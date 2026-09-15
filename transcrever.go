package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

/* O SEGUNDO ESTÁGIO DA ESTEIRA: quem transcreve o que já está no disco.

   Um transcritor só. Transcrever é CPU, não rede: dois whisper-cli ao mesmo
   tempo num notebook não terminam antes, só deixam a máquina inteira lenta.

   A fila é a tabela `transcricoes`, com o contrato da de downloads: reivindicar
   num UPDATE só, tentativa contada na reivindicação, desfecho escrito com
   contexto próprio. O que muda é o que fazer com cada erro, e quem decide é
   transcricao.Classificar — a esteira não conhece whisper nem HTTP:

     Passageiro    espera crescente, até o máximo de tentativas
     Definitivo    falhou, sem repetir
     Configuracao  devolve a tentativa e pausa: nenhum áudio vai dar até alguém
                   instalar o que falta, e gastar tentativas agora condenaria a
                   fila inteira por causa de um ffmpeg ausente
     Cancelado     devolve, sem espera

   Limite de uso do serviço também não gasta tentativa: não é culpa do áudio, e
   uma fila de cem numa conta gratuita desistiria de quase todos. */

type tarefaTranscricao struct {
	mensagem, conversa, arquivo, sha, mime string
	segundos, tentativas                   int
	temAnexo                               bool
}

func (e *Esteira) acordarTranscritor() {
	select {
	case e.acordarTranscricao <- struct{}{}:
	default:
	}
}

func (e *Esteira) transcreverSempre(ctx context.Context) {
	defer e.wg.Done()
	t := time.NewTicker(e.cfg.intervalo)
	defer t.Stop()
	for {
		// Antes da fila: se o vocabulário ganhou uma correção, o que já foi
		// transcrito também ganha. Ver vocabulario.go.
		e.recorrigirSeMudou(ctx)
		for ctx.Err() == nil && e.transcreverUma(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-e.acordarTranscricao:
		case <-t.C:
		}
	}
}

// Reivindica e transcreve UMA. Devolve false quando não havia o que fazer, ou
// quando o motor não está pronto.
func (e *Esteira) transcreverUma(ctx context.Context) bool {
	if e.cfg.motor == nil || !e.motorPronto(ctx) {
		return false
	}
	var t tarefaTranscricao
	// Ao vivo antes do histórico, e o mais novo primeiro — a mesma ordem do download.
	err := e.banco.db.QueryRowContext(ctx, `
		UPDATE transcricoes SET estado = 'transcrevendo', tentativas = tentativas + 1
		 WHERE rowid = (SELECT t.rowid FROM transcricoes t
		                  JOIN midias md ON md.mensagem = t.mensagem AND md.conversa = t.conversa
		                 WHERE t.estado = 'pendente' AND t.proxima_em <= ? AND md.arquivo IS NOT NULL
		                 ORDER BY md.origem, md.em DESC LIMIT 1)
		RETURNING mensagem, conversa, tentativas`, e.agora().Unix()).Scan(&t.mensagem, &t.conversa, &t.tentativas)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err == nil {
		err = e.banco.db.QueryRowContext(ctx, `
			SELECT COALESCE(arquivo, ''), COALESCE(sha256, ''), COALESCE(mime, ''), COALESCE(segundos, 0), anexo IS NOT NULL
			  FROM midias WHERE mensagem = ? AND conversa = ?`, t.mensagem, t.conversa).
			Scan(&t.arquivo, &t.sha, &t.mime, &t.segundos, &t.temAnexo)
	}
	if err != nil {
		if t.mensagem != "" {
			e.devolverTranscricao(t, 0, "")
		}
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "!! esteira: reivindicar transcrição: %v\n", err)
		}
		return false
	}
	e.transcrever(ctx, t)
	return true
}

func (e *Esteira) transcrever(ctx context.Context, t tarefaTranscricao) {
	// Encaminhado: o mesmo conteúdo já virou texto em outra mensagem.
	if e.copiarTranscricao(ctx, t) {
		return
	}
	abs := filepath.Join(e.dir, filepath.FromSlash(t.arquivo))
	if !jaNoDisco(abs, 0) {
		e.arquivoSumiu(t)
		return
	}
	// A dica leva o nome do contato; o corretor troca os erros já conhecidos.
	// O texto do motor fica guardado como veio. Ver vocabulario.go.
	dica, corretor := vocabularioDa(ctx, e.dir, e.banco, t.conversa)
	r, err := e.cfg.motor.Transcrever(ctx,
		transcricao.Audio{Caminho: abs, Mime: t.mime, Duracao: time.Duration(t.segundos) * time.Second},
		transcricao.Pedido{Idioma: e.cfg.idioma, Dica: dica.Texto})

	var limite *transcricao.ErroLimite
	switch classe := transcricao.Classificar(err); {
	case classe == transcricao.Ok:
		e.escrever(`UPDATE transcricoes SET estado = 'feita', texto = ?, bruto = ?, dica = NULLIF(?, ''),
		                    idioma = ?, motor = ?, modelo = ?, levou_ms = ?, feita_em = ?, erro = NULL, proxima_em = 0
		             WHERE mensagem = ? AND conversa = ?`,
			corretor.Corrigir(r.Texto), r.Texto, dica.Texto,
			r.Idioma, r.Motor, r.Modelo, r.Levou.Milliseconds(), e.agora().Unix(), t.mensagem, t.conversa)
	case classe == transcricao.Cancelado, ctx.Err() != nil:
		e.devolverTranscricao(t, 0, "")
	case classe == transcricao.Configuracao:
		e.devolverTranscricao(t, 0, "")
		e.anotarProblema(err.Error())
	case errors.As(err, &limite):
		e.devolverTranscricao(t, max(limite.Depois, time.Minute), err.Error())
	case classe == transcricao.Definitivo:
		e.encerrarTranscricao(t, err.Error())
	default:
		e.adiarTranscricao(t, err)
	}
}

func (e *Esteira) copiarTranscricao(ctx context.Context, t tarefaTranscricao) bool {
	if t.sha == "" {
		return false
	}
	var texto, idioma, motor, modelo string
	var bruto, dica sql.NullString
	err := e.banco.db.QueryRowContext(ctx, `
		SELECT COALESCE(o.texto, ''), COALESCE(o.idioma, ''), COALESCE(o.motor, ''), COALESCE(o.modelo, ''), o.bruto, o.dica
		  FROM transcricoes o JOIN midias md ON md.mensagem = o.mensagem AND md.conversa = o.conversa
		 WHERE md.sha256 = ? AND o.estado = 'feita' LIMIT 1`, t.sha).Scan(&texto, &idioma, &motor, &modelo, &bruto, &dica)
	if err != nil {
		return false
	}
	e.escrever(`UPDATE transcricoes SET estado = 'feita', texto = ?, bruto = ?, dica = ?, idioma = ?, motor = ?, modelo = ?,
	                    levou_ms = 0, feita_em = ?, erro = NULL, proxima_em = 0
	             WHERE mensagem = ? AND conversa = ?`,
		texto, bruto, dica, idioma, motor, modelo, e.agora().Unix(), t.mensagem, t.conversa)
	return true
}

// O arquivo não está no disco: apagado à mão, ou o diretório mudou de lugar sem
// a pasta midia/. Com a chave guardada, baixa de novo; sem ela, não há volta.
func (e *Esteira) arquivoSumiu(t tarefaTranscricao) {
	if !t.temAnexo {
		e.encerrarTranscricao(t, "o arquivo do áudio sumiu do disco, e sem a chave não há como baixar de novo")
		return
	}
	e.escrever(`UPDATE midias SET estado = 'pendente', arquivo = NULL, proxima_em = 0, erro = NULL
	             WHERE mensagem = ? AND conversa = ?`, t.mensagem, t.conversa)
	e.devolverTranscricao(t, 0, "")
	e.Acordar()
}

/* Conferir o motor custa um LookPath e um Stat. A cada áudio seria desperdício;
   só quando algo falha, tarde demais — a fila já teria gastado as tentativas. O
   resultado vale `reverificar`: instalar o ffmpeg com o daemon no ar volta a
   transcrever em até cinco minutos, sem reiniciar. */

func (e *Esteira) motorPronto(ctx context.Context) bool {
	agora := e.agora()
	if !e.conferidoEm.IsZero() && agora.Sub(e.conferidoEm) < e.cfg.reverificar {
		return e.problema == ""
	}
	problema := ""
	if v, ok := e.cfg.motor.(transcricao.Verificador); ok {
		if err := v.Verificar(ctx); err != nil {
			problema = err.Error()
		}
	}
	if problema != e.problema || e.conferidoEm.IsZero() {
		e.anotarProblema(problema)
	}
	e.conferidoEm = agora
	return problema == ""
}

// O problema vai para o banco, e não só para o log: quem pergunta ao
// `estado_da_ponte` por que os áudios não andam não está olhando esta janela.
func (e *Esteira) anotarProblema(problema string) {
	if problema != "" && problema != e.problema {
		fmt.Fprintf(os.Stderr, "!! transcrição parada: %s\n   rode `whatsapp-reader verificar` para ver o que falta\n", problema)
	}
	e.problema, e.conferidoEm = problema, e.agora()
	ctx, cancela := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancela()
	e.banco.Anotar(ctx, "transcricao_problema", problema)
}

// Volta para a fila SEM gastar a tentativa.
func (e *Esteira) devolverTranscricao(t tarefaTranscricao, espera time.Duration, motivo string) {
	var proxima int64
	if espera > 0 {
		proxima = e.agora().Add(espera).Unix()
	}
	var erro any
	if motivo != "" {
		erro = motivo
	}
	e.escrever(`UPDATE transcricoes SET estado = 'pendente', tentativas = MAX(tentativas - 1, 0), proxima_em = ?, erro = ?
	             WHERE mensagem = ? AND conversa = ?`, proxima, erro, t.mensagem, t.conversa)
}

func (e *Esteira) adiarTranscricao(t tarefaTranscricao, err error) {
	if t.tentativas >= e.cfg.maxTranscricoes {
		e.encerrarTranscricao(t, fmt.Sprintf("desisti depois de %d tentativas — %v", t.tentativas, err))
		return
	}
	e.escrever(`UPDATE transcricoes SET estado = 'pendente', proxima_em = ?, erro = ?
	             WHERE mensagem = ? AND conversa = ?`,
		e.agora().Add(atraso(e.cfg.esperaTranscricao, t.tentativas)).Unix(), err.Error(), t.mensagem, t.conversa)
}

func (e *Esteira) encerrarTranscricao(t tarefaTranscricao, motivo string) {
	e.escrever(`UPDATE transcricoes SET estado = 'falhou', erro = ? WHERE mensagem = ? AND conversa = ?`,
		motivo, t.mensagem, t.conversa)
	fmt.Fprintf(os.Stderr, "!! transcrição do áudio %s: falhou — %s\n", t.mensagem, motivo)
}
