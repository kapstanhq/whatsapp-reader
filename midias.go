package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/kapstanhq/whatsapp-reader/midia"
)

/* O ANEXO GUARDADO, antes de qualquer download.

   O WhatsApp entrega o histórico UMA vez, e a chave de cada anexo vem junto.
   Até aqui a ponte gravava só o rótulo "[áudio]" e jogava a chave fora: nenhum
   áudio que passou por ela podia ser baixado depois. Em 15/09/2026 os áudios do
   seu João sobre a reforma das calhas ficaram ilegíveis por isso.

   Então a chave passa a ser guardada NA HORA, dentro do handler de eventos, com
   um INSERT e nada mais — sem rede, sem arquivo. O whatsmeow processa as
   mensagens em série, e qualquer coisa lenta ali atrasa todas as outras. Quem
   baixa é a esteira, depois, lendo esta tabela.

   A política decide o que entra na fila. Guardar a chave é barato e não volta
   atrás; baixar e transcrever custa rede, disco e CPU. Por isso quase tudo é
   guardado e só o áudio de conversa individual recente vai para a fila. */

const (
	midiaGuardada     = "guardada"     // chave guardada, fora da fila (grupo, antigo, não é áudio)
	midiaIgnorada     = "ignorada"     // nem a chave fica (visualização única)
	midiaPendente     = "pendente"     // na fila para baixar
	midiaBaixando     = "baixando"     // um worker está com ela
	midiaPedida       = "pedida"       // o link venceu, esperando o celular reenviar
	midiaBaixada      = "baixada"      // o arquivo está no disco
	midiaIndisponivel = "indisponivel" // o celular não tem mais
	midiaFalhou       = "falhou"       // desistiu depois das tentativas
)

const (
	origemAoVivo    = 0
	origemHistorico = 1
)

// Quantos dias para trás um áudio ainda entra na fila. O primeiro history sync
// traz meses de conversa, e o link de quase tudo já venceu: pedir tudo ao
// celular de uma vez é tráfego que parece robô e fila que não acaba num
// notebook sem placa de vídeo.
const diasDeMidiaPadrao = 7

func diasDeMidia(getenv func(string) string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(getenv("WHATSAPP_READER_MIDIA_DIAS"))); err == nil && n > 0 {
		return n
	}
	return diasDeMidiaPadrao
}

type decisao struct {
	registrar    bool
	estado       string
	motivo       string
	guardarChave bool
}

func politica(conversa string, a midia.Anexo, visualizacaoUnica bool, em, agora time.Time, dias int) decisao {
	jid, err := types.ParseJID(conversa)
	if err != nil || jid.IsEmpty() {
		return decisao{}
	}
	switch jid.Server {
	case types.BroadcastServer, types.NewsletterServer:
		// Status, lista de transmissão e canal: não é conversa de ninguém com o corretor.
		return decisao{}
	}
	if visualizacaoUnica {
		// Quem mandou escolheu que não ficasse. A linha existe só para o
		// rótulo dizer por que não há transcrição.
		return decisao{registrar: true, estado: midiaIgnorada, motivo: "visualização única"}
	}
	d := decisao{registrar: true, estado: midiaGuardada, guardarChave: true}
	switch {
	case a.Tipo != midia.Audio:
		d.motivo = "não é áudio"
	case jid.Server == types.GroupServer:
		d.motivo = "grupo"
	case jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer:
		d.motivo = "conversa sem suporte"
	case em.Before(agora.AddDate(0, 0, -dias)):
		d.motivo = "antigo"
	default:
		d.estado, d.motivo = midiaPendente, ""
	}
	return d
}

// O gravador é o que o handler chama, ao vivo e no histórico. `acordar` avisa a
// esteira que há trabalho novo; pode ser nil.
type gravador struct {
	banco   *Banco
	dias    int
	agora   func() time.Time
	acordar func()
}

func (g *gravador) gravar(ctx context.Context, conversa string, evt *events.Message, origem int) {
	gravarUma(ctx, g.banco, evt.Info.ID, conversa, evt.Info.Sender.String(),
		evt.Info.PushName, evt.Info.IsFromMe, evt.Info.Timestamp, evt.Message)

	a, ok := midia.Extrair(evt.Message)
	if !ok || evt.Info.ID == "" || conversa == "" {
		return
	}
	d := politica(conversa, a, evt.IsViewOnce, evt.Info.Timestamp, g.agora(), g.dias)
	if !d.registrar {
		return
	}
	novo, err := g.banco.GravarMidia(ctx, registroMidia{
		Mensagem: evt.Info.ID, Conversa: conversa, Anexo: a, Em: evt.Info.Timestamp,
		Origem: origem, Estado: d.estado, Motivo: d.motivo, GuardarChave: d.guardarChave,
	})
	if err != nil {
		// A mensagem já está gravada; perder o anexo é perder só o download.
		fmt.Fprintf(os.Stderr, "!! o anexo da mensagem %s não foi registrado: %v\n", evt.Info.ID, err)
		return
	}
	if novo && d.estado == midiaPendente && g.acordar != nil {
		g.acordar()
	}
}

type registroMidia struct {
	Mensagem, Conversa string
	Anexo              midia.Anexo
	Em                 time.Time
	Origem             int
	Estado, Motivo     string
	GuardarChave       bool
}

/* Duplicata é o caso comum: o history sync repete mensagem que já chegou ao
   vivo, e reiniciar o daemon repete o sync. O que já existe NÃO muda — a linha
   pode estar no meio do download, ou com o caminho novo que o celular acabou de
   mandar, e uma cópia velha por cima desfaria isso. A única exceção é a linha
   que já desistiu (`falhou`): uma chave que chega de novo é motivo para tentar
   outra vez.

   Devolve se a linha é nova (ou foi ressuscitada). */

func (b *Banco) GravarMidia(ctx context.Context, r registroMidia) (bool, error) {
	var anexo any
	if r.GuardarChave {
		dados, err := r.Anexo.Bytes()
		if err != nil {
			return false, err
		}
		anexo = dados
	}
	var sha, motivo any
	if s := r.Anexo.SHA256(); len(s) > 0 {
		sha = hex.EncodeToString(s)
	}
	if r.Motivo != "" {
		motivo = r.Motivo
	}
	res, err := b.db.ExecContext(ctx, `
		INSERT INTO midias (mensagem, conversa, tipo, mime, segundos, voz, tamanho, sha256, anexo, em, origem, estado, erro)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mensagem, conversa) DO UPDATE SET
		  anexo = excluded.anexo, estado = excluded.estado, erro = NULL, tentativas = 0, proxima_em = 0
		WHERE midias.estado = 'falhou' AND excluded.estado = 'pendente' AND excluded.anexo IS NOT NULL`,
		r.Mensagem, r.Conversa, string(r.Anexo.Tipo), r.Anexo.Mime(), int64(r.Anexo.Segundos()),
		r.Anexo.Voz(), int64(r.Anexo.Tamanho()), sha, anexo, r.Em.Unix(), r.Origem, r.Estado, motivo)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// O retrato dos áudios para o `estado_da_ponte`. Lido do banco, e não da
// memória do daemon, pelo mesmo motivo da batida: o `mcp` precisa responder
// também quando o daemon está fora.
type ResumoAudios struct {
	Total, Transcritos, NaFila, SemVolta, Falharam, NaoBaixados int
	Bytes                                                       int64
	MaisAntigoNaFila                                            time.Time // quando chegou; zero sem fila
}

func (b *Banco) ResumoAudios(ctx context.Context) ResumoAudios {
	var r ResumoAudios
	var maisAntigo int64
	b.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		  COALESCE(SUM(CASE WHEN t.estado = 'feita' THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN md.estado IN ('pendente','baixando','pedida')
		                      OR t.estado IN ('pendente','transcrevendo') THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN md.estado = 'indisponivel' THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN md.estado = 'falhou' OR t.estado = 'falhou' THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN md.estado IN ('guardada','ignorada') THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN md.arquivo IS NOT NULL THEN COALESCE(md.tamanho, 0) ELSE 0 END), 0),
		  COALESCE(MIN(CASE WHEN md.estado IN ('pendente','baixando','pedida')
		                      OR t.estado IN ('pendente','transcrevendo') THEN md.em END), 0)
		  FROM midias md
		  LEFT JOIN transcricoes t ON t.mensagem = md.mensagem AND t.conversa = md.conversa
		 WHERE md.tipo = 'audio'`).
		Scan(&r.Total, &r.Transcritos, &r.NaFila, &r.SemVolta, &r.Falharam, &r.NaoBaixados, &r.Bytes, &maisAntigo)
	if maisAntigo > 0 {
		r.MaisAntigoNaFila = time.Unix(maisAntigo, 0)
	}
	return r
}

func (r ResumoAudios) Linha() string {
	partes := []string{
		fmt.Sprintf("%d transcritos", r.Transcritos),
		fmt.Sprintf("%d na fila", r.NaFila),
	}
	// Quatro na fila, o mais antigo de dois minutos, é fila andando; de dois
	// dias, é fila parada. Abaixo de um minuto a idade não diz nada.
	if d := time.Since(r.MaisAntigoNaFila); !r.MaisAntigoNaFila.IsZero() && d >= time.Minute {
		partes[1] += " (o mais antigo chegou há " + humano(d) + ")"
	}
	if r.SemVolta > 0 {
		partes = append(partes, fmt.Sprintf("%d sem volta", r.SemVolta))
	}
	if r.Falharam > 0 {
		partes = append(partes, fmt.Sprintf("%d falharam", r.Falharam))
	}
	if r.NaoBaixados > 0 {
		partes = append(partes, fmt.Sprintf("%d não baixados (grupo, antigo ou visualização única)", r.NaoBaixados))
	}
	if r.Bytes > 0 {
		partes = append(partes, tamanhoHumano(r.Bytes))
	}
	return "áudios: " + strings.Join(partes, " · ")
}

func tamanhoHumano(n int64) string {
	switch {
	case n < 1<<20:
		return fmt.Sprintf("%d KB", (n+1023)>>10)
	case n < 10<<20:
		return strings.Replace(fmt.Sprintf("%.1f MB", float64(n)/(1<<20)), ".", ",", 1)
	}
	return fmt.Sprintf("%d MB", n>>20)
}
