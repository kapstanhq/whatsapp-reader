package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/kapstanhq/whatsapp-reader/midia"
	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

/* A ESTEIRA: quem baixa o que o handler só registrou.

   O banco é a fila. O canal `acordar` é só campainha — se ela tocar sem ninguém
   ouvir, o ticker acha o trabalho do mesmo jeito, e um reinício não perde nada
   porque nada mora na memória. Uma fila em canal (como em outras pontes) perde
   o que estava dentro quando o processo cai, e descarta o que não cabe.

   A tentativa é contada NA REIVINDICAÇÃO, não no fim. Um áudio que derruba o
   daemon no meio do download volta para a fila com a conta andada, e desiste
   depois do máximo em vez de derrubar o daemon a cada subida.

   Link vencido é o caso normal do histórico: o que chegou há dias já não baixa.
   O caminho é pedir ao celular que suba de novo — no máximo 20 pedidos por
   hora, contados no BANCO, pela mesma razão das travas de envio: reiniciar o
   daemon não pode zerar o teto. A resposta chega sozinha, como
   *events.MediaRetry, e é tratada no handler só com banco; quem baixa de novo
   é a esteira. */

type clienteMidia interface {
	midia.Baixador
	midia.Pedinte
	IsConnected() bool
	IsLoggedIn() bool
}

type configEsteira struct {
	baixadores     int           // goroutines de download (rede, não CPU)
	maxTentativas  int           // por mídia, contando pedidos ao celular
	baseEspera     time.Duration // primeira espera depois de um erro passageiro
	pedidosPorHora int           // teto de pedidos de link novo ao celular
	intervalo      time.Duration // o ticker que acha backoff vencido
	dias           int           // janela de WHATSAPP_READER_MIDIA_DIAS

	motor             transcricao.Motor // nil: transcrição desligada
	idioma            string
	maxTranscricoes   int           // tentativas por transcrição
	esperaTranscricao time.Duration // primeira espera depois de um erro passageiro
	reverificar       time.Duration // quanto tempo vale a conferência do motor

	guardarDias int // WHATSAPP_READER_MIDIA_GUARDAR_DIAS; zero guarda os arquivos para sempre
}

func configEsteiraPadrao(dias int) configEsteira {
	return configEsteira{
		baixadores: 2, maxTentativas: 6, baseEspera: 2 * time.Minute,
		pedidosPorHora: 20, intervalo: 30 * time.Second, dias: dias,
		idioma: "pt", maxTranscricoes: 3, esperaTranscricao: 10 * time.Minute, reverificar: 5 * time.Minute,
	}
}

type Esteira struct {
	banco *Banco
	dir   string
	wa    clienteMidia
	cfg   configEsteira
	agora func() time.Time

	acordar            chan struct{} // campainha dos downloads
	acordarTranscricao chan struct{} // e a do transcritor: uma só tocaria para o worker errado
	cancelar           context.CancelFunc
	wg                 sync.WaitGroup
	fechar             sync.Once

	// Só o transcritor lê e escreve estes dois — e ele é um só.
	problema    string
	conferidoEm time.Time
}

func novaEsteira(dir string, b *Banco, wa clienteMidia, cfg configEsteira, agora func() time.Time) *Esteira {
	return &Esteira{banco: b, dir: dir, wa: wa, cfg: cfg, agora: agora,
		acordar: make(chan struct{}, 1), acordarTranscricao: make(chan struct{}, 1)}
}

// AbrirEsteira recupera o que uma queda deixou pela metade e põe os workers para rodar.
func AbrirEsteira(ctx context.Context, dir string, b *Banco, wa clienteMidia, cfg configEsteira) (*Esteira, error) {
	e := novaEsteira(dir, b, wa, cfg, time.Now)
	if err := e.iniciar(ctx); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Esteira) iniciar(ctx context.Context) error {
	if err := e.recuperar(ctx); err != nil {
		return err
	}
	vivo, cancelar := context.WithCancel(context.Background())
	e.cancelar = cancelar
	for i := 0; i < e.cfg.baixadores; i++ {
		e.wg.Add(1)
		go e.baixarSempre(vivo)
	}
	if e.cfg.motor != nil {
		e.wg.Add(1)
		go e.transcreverSempre(vivo)
	}
	if e.cfg.guardarDias > 0 {
		e.wg.Add(1)
		go e.limparSempre(vivo)
	}
	return nil
}

// Acordar avisa que há trabalho. Nunca bloqueia: é chamado de dentro do handler.
func (e *Esteira) Acordar() {
	select {
	case e.acordar <- struct{}{}:
	default:
	}
}

// Fechar cancela o que está em curso e espera os workers devolverem as linhas.
// Espera no máximo 5 s: o que sobrar, a recuperação da próxima subida resolve.
func (e *Esteira) Fechar() {
	e.fechar.Do(func() {
		if e.cancelar == nil {
			return
		}
		e.cancelar()
		pronto := make(chan struct{})
		go func() { e.wg.Wait(); close(pronto) }()
		select {
		case <-pronto:
		case <-time.After(5 * time.Second):
		}
	})
}

/* O que uma queda deixa para trás: linha marcada "baixando" sem ninguém
   baixando, arquivo .parcial, pedido ao celular que nunca teve resposta. E, se
   o corretor aumentou WHATSAPP_READER_MIDIA_DIAS, áudio guardado como antigo
   que agora cabe na janela. */

func (e *Esteira) recuperar(ctx context.Context) error {
	agora := e.agora()
	passos := []struct {
		sql  string
		args []any
	}{
		{`UPDATE midias SET estado = 'pendente' WHERE estado = 'baixando'`, nil},
		{`UPDATE transcricoes SET estado = 'pendente' WHERE estado = 'transcrevendo'`, nil},
		{`UPDATE midias SET estado = 'pendente', erro = NULL, proxima_em = 0
		   WHERE estado = 'guardada' AND erro = 'antigo' AND tipo = 'audio' AND anexo IS NOT NULL AND em >= ?`,
			[]any{agora.AddDate(0, 0, -e.cfg.dias).Unix()}},
	}
	for _, p := range passos {
		if _, err := e.banco.db.ExecContext(ctx, p.sql, p.args...); err != nil {
			return fmt.Errorf("recuperar a esteira de mídia: %w", err)
		}
	}
	e.devolverPedidasVelhas(ctx)
	// O que uma queda deixa no disco: .parcial de download e pasta de trabalho do
	// whisper. Na subida, ninguém mais está usando nenhum dos dois.
	os.RemoveAll(filepath.Join(e.dir, "midia", ".tmp"))
	filepath.WalkDir(filepath.Join(e.dir, "midia"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".parcial") {
			os.Remove(p)
		}
		return nil
	})
	return nil
}

// Pedido ao celular sem resposta em 24 h volta para a fila: o celular pode ter
// ficado desligado, e a próxima tentativa pede de novo.
func (e *Esteira) devolverPedidasVelhas(ctx context.Context) {
	e.banco.db.ExecContext(ctx, `
		UPDATE midias SET estado = 'pendente', proxima_em = 0
		 WHERE estado = 'pedida' AND COALESCE(pedida_em, 0) < ?`,
		e.agora().Add(-24*time.Hour).Unix())
}

func (e *Esteira) baixarSempre(ctx context.Context) {
	defer e.wg.Done()
	t := time.NewTicker(e.cfg.intervalo)
	defer t.Stop()
	for {
		for ctx.Err() == nil && e.baixarUma(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-e.acordar:
		case <-t.C:
			e.devolverPedidasVelhas(ctx)
		}
	}
}

type tarefaMidia struct {
	mensagem, conversa, tipo, sha string
	anexo                         []byte
	tentativas                    int
	tamanho                       int64
}

// Reivindica e processa UMA mídia. Devolve false quando não havia o que fazer.
func (e *Esteira) baixarUma(ctx context.Context) bool {
	if !e.wa.IsConnected() || !e.wa.IsLoggedIn() {
		return false
	}
	agora := e.agora()
	var t tarefaMidia
	// Um UPDATE só, com a escolha dentro: dois workers nunca pegam a mesma linha.
	// Ao vivo antes do histórico, e o mais novo primeiro.
	err := e.banco.db.QueryRowContext(ctx, `
		UPDATE midias SET estado = 'baixando', tentativas = tentativas + 1
		 WHERE rowid = (SELECT rowid FROM midias
		                 WHERE estado = 'pendente' AND tipo = 'audio' AND anexo IS NOT NULL AND proxima_em <= ?
		                 ORDER BY origem, em DESC LIMIT 1)
		RETURNING mensagem, conversa, tipo, COALESCE(sha256, ''), anexo, tentativas, COALESCE(tamanho, 0)`,
		agora.Unix()).Scan(&t.mensagem, &t.conversa, &t.tipo, &t.sha, &t.anexo, &t.tentativas, &t.tamanho)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "!! esteira: reivindicar download: %v\n", err)
		}
		return false
	}
	e.processar(ctx, t, agora)
	return true
}

func (e *Esteira) processar(ctx context.Context, t tarefaMidia, agora time.Time) {
	a, err := midia.Ler(midia.Tipo(t.tipo), t.anexo)
	if err != nil {
		e.encerrar(t, midiaFalhou, "o anexo guardado está ilegível: "+err.Error())
		return
	}
	rel := caminhoDaMidia(t.sha, a)
	if rel == "" {
		e.encerrar(t, midiaFalhou, "o anexo não traz o hash do conteúdo, e sem ele o download nunca confere")
		return
	}
	abs := filepath.Join(e.dir, filepath.FromSlash(rel))
	// O nome é o hash do conteúdo: áudio encaminhado que já está no disco não se baixa de novo.
	if jaNoDisco(abs, t.tamanho) {
		e.concluir(t, rel, agora)
		return
	}
	err = midia.Baixar(ctx, e.wa, a, abs)
	switch {
	case err == nil:
		e.concluir(t, rel, agora)
	case ctx.Err() != nil, errors.Is(err, context.Canceled):
		// Ctrl+C no meio do download: não foi culpa da mídia, não conta tentativa.
		e.devolver(t, agora, 0, "")
	case errors.Is(err, midia.ErrVencido):
		e.pedirAoCelular(ctx, t, a, agora)
	case errors.Is(err, midia.ErrCorrompido):
		e.encerrar(t, midiaFalhou, "o arquivo não confere com a chave: "+err.Error())
	case discoCheio(err):
		e.devolver(t, agora, 10*time.Minute, "disco cheio — downloads pausados")
	default:
		e.adiar(t, agora, "download: "+err.Error())
	}
}

func (e *Esteira) pedirAoCelular(ctx context.Context, t tarefaMidia, a midia.Anexo, agora time.Time) {
	if t.tentativas >= e.cfg.maxTentativas {
		e.encerrar(t, midiaIndisponivel, "o link venceu e o celular não reenviou depois de várias tentativas")
		return
	}
	var pedidos int
	e.banco.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM midias WHERE pedida_em > ?`,
		agora.Add(-time.Hour).Unix()).Scan(&pedidos)
	if pedidos >= e.cfg.pedidosPorHora {
		e.devolver(t, agora, time.Hour, fmt.Sprintf(
			"o link venceu; já foram %d pedidos ao celular nesta hora, que é o teto", pedidos))
		return
	}
	info, err := e.infoDaMensagem(ctx, t)
	if err != nil {
		e.encerrar(t, midiaFalhou, err.Error())
		return
	}
	if err := midia.PedirAoCelular(ctx, e.wa, a, info); err != nil {
		if errors.Is(err, midia.ErrCorrompido) {
			e.encerrar(t, midiaFalhou, err.Error())
			return
		}
		e.adiar(t, agora, "pedir ao celular: "+err.Error())
		return
	}
	e.escrever(`UPDATE midias SET estado = 'pedida', pedida_em = ?, erro = NULL
	             WHERE mensagem = ? AND conversa = ?`, agora.Unix(), t.mensagem, t.conversa)
}

// O pedido ao celular precisa dizer de quem é a mensagem; isso mora em `mensagens`.
func (e *Esteira) infoDaMensagem(ctx context.Context, t tarefaMidia) (types.MessageInfo, error) {
	chat, err := types.ParseJID(t.conversa)
	if err != nil || chat.IsEmpty() {
		return types.MessageInfo{}, fmt.Errorf("conversa ilegível %q para pedir ao celular", t.conversa)
	}
	var remetente string
	var deMim bool
	e.banco.db.QueryRowContext(ctx, `
		SELECT COALESCE(remetente, ''), de_mim FROM mensagens WHERE id = ? AND conversa = ?`,
		t.mensagem, t.conversa).Scan(&remetente, &deMim)
	info := types.MessageInfo{ID: t.mensagem, MessageSource: types.MessageSource{
		Chat: chat, IsFromMe: deMim, IsGroup: chat.Server == types.GroupServer,
	}}
	if remetente != "" {
		if s, err := types.ParseJID(remetente); err == nil {
			info.Sender = s
		}
	}
	return info, nil
}

// RespostaDoCelular é chamada no handler de eventos: só banco e AES-GCM.
func (e *Esteira) RespostaDoCelular(ctx context.Context, evt *events.MediaRetry) {
	if evt == nil {
		return
	}
	type candidata struct {
		conversa, tipo string
		anexo          []byte
		tentativas     int
	}
	rows, err := e.banco.db.QueryContext(ctx, `
		SELECT conversa, tipo, anexo, tentativas FROM midias
		 WHERE mensagem = ? AND estado = 'pedida' AND anexo IS NOT NULL`, string(evt.MessageID))
	if err != nil {
		return
	}
	var candidatas []candidata
	for rows.Next() {
		var c candidata
		if rows.Scan(&c.conversa, &c.tipo, &c.anexo, &c.tentativas) == nil {
			candidatas = append(candidatas, c)
		}
	}
	rows.Close()

	agora := e.agora()
	// O evento traz só o ID da mensagem, e o ChatID pode vir na forma @lid ou na
	// do telefone. Quem decide de qual linha é a resposta é a chave: ela só abre
	// com a do anexo certo.
	for _, c := range candidatas {
		a, err := midia.Ler(midia.Tipo(c.tipo), c.anexo)
		if err != nil {
			continue
		}
		novo, err := midia.RespostaDoCelular(evt, a)
		if errors.Is(err, midia.ErrOutroAnexo) {
			continue
		}
		t := tarefaMidia{mensagem: string(evt.MessageID), conversa: c.conversa, tipo: c.tipo, tentativas: c.tentativas}
		switch {
		case err == nil:
			dados, err := novo.Bytes()
			if err != nil {
				e.encerrar(t, midiaFalhou, "não consegui guardar o caminho novo: "+err.Error())
				return
			}
			e.escrever(`UPDATE midias SET anexo = ?, estado = 'pendente', proxima_em = 0, erro = NULL
			             WHERE mensagem = ? AND conversa = ? AND estado = 'pedida'`, dados, t.mensagem, t.conversa)
			e.Acordar()
		case errors.Is(err, midia.ErrIndisponivel):
			e.encerrar(t, midiaIndisponivel, "o celular não tem mais este áudio")
		case errors.Is(err, midia.ErrCorrompido):
			e.encerrar(t, midiaFalhou, err.Error())
		default:
			e.adiar(t, agora, "resposta do celular: "+err.Error())
		}
		return
	}
}

// -- os desfechos -----------------------------------------------------------

/* Os desfechos escrevem com contexto próprio, e não com o do worker: o
   download pode terminar no exato instante do Ctrl+C, e o resultado precisa
   chegar ao banco mesmo assim. */

func (e *Esteira) escrever(sql string, args ...any) {
	ctx, cancela := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancela()
	if _, err := e.banco.db.ExecContext(ctx, sql, args...); err != nil {
		fmt.Fprintf(os.Stderr, "!! esteira: gravar desfecho: %v\n", err)
	}
}

// Baixada. A transcrição pendente nasce ANTES da troca de estado: se cair entre
// as duas, a recuperação baixa de novo (o arquivo já está lá) e chega aqui outra vez.
func (e *Esteira) concluir(t tarefaMidia, rel string, agora time.Time) {
	if t.tipo == string(midia.Audio) {
		e.escrever(`INSERT INTO transcricoes (mensagem, conversa) VALUES (?, ?) ON CONFLICT DO NOTHING`,
			t.mensagem, t.conversa)
	}
	e.escrever(`UPDATE midias SET estado = 'baixada', arquivo = ?, baixada_em = ?, erro = NULL
	             WHERE mensagem = ? AND conversa = ?`, rel, agora.Unix(), t.mensagem, t.conversa)
	e.acordarTranscritor()
}

// Volta para a fila SEM gastar a tentativa: cancelamento, disco cheio, teto de pedidos.
func (e *Esteira) devolver(t tarefaMidia, agora time.Time, espera time.Duration, motivo string) {
	var proxima int64
	if espera > 0 {
		proxima = agora.Add(espera).Unix()
	}
	var erro any
	if motivo != "" {
		erro = motivo
	}
	e.escrever(`UPDATE midias SET estado = 'pendente', tentativas = MAX(tentativas - 1, 0), proxima_em = ?, erro = ?
	             WHERE mensagem = ? AND conversa = ?`, proxima, erro, t.mensagem, t.conversa)
}

// Erro passageiro: tenta de novo mais tarde, até o máximo.
func (e *Esteira) adiar(t tarefaMidia, agora time.Time, motivo string) {
	if t.tentativas >= e.cfg.maxTentativas {
		e.encerrar(t, midiaFalhou, fmt.Sprintf("desisti depois de %d tentativas — %s", t.tentativas, motivo))
		return
	}
	e.escrever(`UPDATE midias SET estado = 'pendente', proxima_em = ?, erro = ?
	             WHERE mensagem = ? AND conversa = ?`,
		agora.Add(atraso(e.cfg.baseEspera, t.tentativas)).Unix(), motivo, t.mensagem, t.conversa)
}

func (e *Esteira) encerrar(t tarefaMidia, estado, motivo string) {
	e.escrever(`UPDATE midias SET estado = ?, erro = ? WHERE mensagem = ? AND conversa = ?`,
		estado, motivo, t.mensagem, t.conversa)
	fmt.Fprintf(os.Stderr, "!! áudio %s: %s — %s\n", t.mensagem, estado, motivo)
}

// A espera dobra a cada tentativa, até 6 h, com ±20% de sorteio: duas mídias
// que falharam juntas não voltam juntas.
func atraso(base time.Duration, tentativa int) time.Duration {
	const teto = 6 * time.Hour
	d := base
	for i := 1; i < tentativa && d < teto; i++ {
		d *= 2
	}
	if d > teto {
		d = teto
	}
	return d - d/5 + time.Duration(rand.Int64N(int64(d)*2/5+1))
}

// midia/ab/abcdef….ogg, relativo ao diretório da ponte: o diretório pode mudar
// de lugar (WHATSAPP_READER_DIR) sem o banco apontar para o vazio.
func caminhoDaMidia(sha string, a midia.Anexo) string {
	if len(sha) < 2 {
		return ""
	}
	return "midia/" + sha[:2] + "/" + sha + a.Extensao()
}

func jaNoDisco(caminho string, tamanho int64) bool {
	st, err := os.Stat(caminho)
	return err == nil && !st.IsDir() && (tamanho == 0 || st.Size() == tamanho)
}
