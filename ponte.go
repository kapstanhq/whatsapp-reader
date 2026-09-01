package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

/* O ELO entre os dois processos. O daemon é quem tem o cliente conectado; o
   `mcp` é curto, só lê o banco e morre junto com o agente. Para o `mcp` mandar
   enviar, ele PEDE ao daemon, por HTTP em 127.0.0.1, com um segredo por sessão.

   NÃO se abre um segundo cliente whatsmeow sobre o mesmo `sessao.db`: o estado
   do ratchet do Signal é de UM dispositivo, e dois clientes sobre ele corrompem
   a sessão — o pareamento cai e o histórico não volta. Um processo tem o
   cliente. O outro pede. */

// As travas do envio. Contam do BANCO, e não da memória: reiniciar o daemon
// era o jeito óbvio de zerar o contador.
const (
	espacoMinimo  = 5 * time.Second  // entre dois envios quaisquer
	distintosHora = 6                // conversas DIFERENTES por hora: é isto que define lote
	tetoHora      = 30               // envios no total por hora
	previaVale    = 10 * time.Minute // tempo de vida da prévia
	previaEspera  = 3 * time.Second  // antes disto, a confirmação não veio de gente
)

type previa struct {
	id       string
	conversa types.JID
	nome     string
	texto    string
	em       time.Time
}

type Elo struct {
	cli     *whatsmeow.Client
	banco   *Banco
	dir     string
	segredo string
	srv     *http.Server

	mu      sync.Mutex
	previas map[string]previa
}

func AbrirElo(dir string, cli *whatsmeow.Client, b *Banco) (*Elo, error) {
	// Porta 0: o SO escolhe. Porta fixa vira superfície conhecida de quem
	// chegar depois, e não ganha nada em troca.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("abrir o elo: %w", err)
	}
	bruto := make([]byte, 24)
	if _, err := rand.Read(bruto); err != nil {
		return nil, err
	}
	e := &Elo{cli: cli, banco: b, dir: dir,
		segredo: hex.EncodeToString(bruto), previas: map[string]previa{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/preparar", e.rota(e.preparar))
	mux.HandleFunc("/enviar", e.rota(e.enviar))
	mux.HandleFunc("/estado", e.rota(e.estado))
	e.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	// O 0600 não faz o trabalho no Windows (a ACL vem do diretório). Quem
	// separa o mcp do corretor de qualquer outro programa local é o segredo.
	j, _ := json.Marshal(map[string]any{
		"porta": lis.Addr().(*net.TCPAddr).Port, "segredo": e.segredo, "pid": os.Getpid(),
	})
	if err := os.WriteFile(filepath.Join(dir, "elo.json"), j, 0o600); err != nil {
		lis.Close()
		return nil, err
	}
	go e.srv.Serve(lis)
	return e, nil
}

func (e *Elo) Fechar() {
	os.Remove(filepath.Join(e.dir, "elo.json"))
	ctx, cancela := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancela()
	e.srv.Shutdown(ctx)
}

func (e *Elo) rota(h func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Loopback é alcançável por qualquer processo do computador.
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Elo")), []byte(e.segredo)) != 1 {
			http.Error(w, "segredo inválido", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		res, err := h(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"erro": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(res)
	}
}

// -- preparar --------------------------------------------------------------

func (e *Elo) preparar(r *http.Request) (any, error) {
	var p struct{ Conversa, Texto string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&p); err != nil {
		return nil, err
	}
	jid, err := destino(p.Conversa)
	if err != nil {
		return nil, err
	}
	p.Texto = strings.TrimSpace(p.Texto)
	if p.Texto == "" {
		return nil, fmt.Errorf("texto vazio")
	}
	nome := comoChamar(e.banco.NomeDe(r.Context(), jid.String()), jid.String())
	// As duas travas respondem AQUI e não no envio: o corretor descobre o
	// limite antes de escrever a mensagem, e não com ela pronta na tela.
	if calou, motivo := e.pediuSilencio(jid); calou {
		return nil, erroSilencio(nome, motivo)
	}
	if err := e.cabeNaJanela(r.Context(), jid.String()); err != nil {
		return nil, err
	}
	pv := previa{id: codigoPrevia(), conversa: jid, nome: nome, texto: p.Texto, em: time.Now()}
	e.mu.Lock()
	e.previas[pv.id] = pv
	e.mu.Unlock()
	if err := e.banco.GravarPrevia(r.Context(), pv.id, jid.String(), nome, p.Texto, pv.em); err != nil {
		return nil, err
	}
	return map[string]any{
		"previa": pv.id, "nome": nome, "conversa": jid.String(),
		"texto": p.Texto, "vale_ate": pv.em.Add(previaVale).Unix(),
	}, nil
}

// -- enviar ----------------------------------------------------------------

func (e *Elo) enviar(r *http.Request) (any, error) {
	var p struct{ Previa, Conversa, Texto string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&p); err != nil {
		return nil, err
	}
	e.mu.Lock()
	pv, ok := e.previas[p.Previa]
	delete(e.previas, p.Previa) // uso único: some ao ser tocada, dê certo ou não
	e.mu.Unlock()
	ctx := r.Context()

	if !ok {
		return nil, fmt.Errorf("prévia %q não existe, já foi usada, ou o daemon reiniciou. "+
			"Chame preparar_envio de novo e mostre a prévia outra vez", p.Previa)
	}
	if idade := time.Since(pv.em); idade > previaVale {
		e.banco.MarcarRecusado(ctx, pv.id, "expirada")
		return nil, fmt.Errorf("a prévia venceu (vale %s). Prepare de novo", previaVale)
	} else if idade < previaEspera {
		// Ninguém lê a prévia e responde em menos de três segundos. Duas
		// chamadas no mesmo turno chegam com milissegundos entre elas.
		e.banco.MarcarRecusado(ctx, pv.id, "confirmada rápido demais")
		return nil, fmt.Errorf("confirmação rápida demais (%s depois da prévia). Mostre a prévia ao corretor e espere ELE responder",
			idade.Round(time.Millisecond))
	}
	if p.Conversa != pv.conversa.String() || p.Texto != pv.texto {
		e.banco.MarcarRecusado(ctx, pv.id, "destino ou texto divergiu da prévia")
		return nil, fmt.Errorf("o que chegou não é o que foi mostrado.\nprévia: %s -> %q\nchegou: %s -> %q",
			pv.conversa, pv.texto, p.Conversa, p.Texto)
	}
	if !e.cli.IsConnected() || !e.cli.IsLoggedIn() {
		return nil, fmt.Errorf("a ponte não está conectada — a janela do `serve` está aberta?")
	}
	// O cliente pode ter escrito enquanto a prévia esperava na tela. Mandar a
	// resposta velha por cima é o erro que mais parece robô para quem recebe.
	if nova, quando, err := e.banco.RecebidaDesde(ctx, pv.conversa.String(), pv.em); err == nil && nova {
		e.banco.MarcarRecusado(ctx, pv.id, "o cliente escreveu depois da prévia")
		return nil, fmt.Errorf("%s escreveu %s depois de a prévia ser feita. "+
			"Leia o que chegou e prepare de novo — o texto pode não servir mais",
			ou(pv.nome, "o cliente"), quando.Format("15:04"))
	}
	if calou, motivo := e.pediuSilencio(pv.conversa); calou {
		e.banco.MarcarRecusado(ctx, pv.id, "na lista de não contatar")
		return nil, erroSilencio(pv.nome, motivo)
	}
	if err := e.cabeNaJanela(ctx, pv.conversa.String()); err != nil {
		return nil, err
	}

	env, cancela := context.WithTimeout(ctx, 60*time.Second)
	defer cancela()
	digitando(env, e.cli, pv.conversa, pv.texto)
	msg := &waE2E.Message{Conversation: proto.String(pv.texto)}
	resp, err := e.cli.SendMessage(env, pv.conversa, msg)
	if err != nil {
		e.banco.MarcarRecusado(context.Background(), pv.id, err.Error())
		return nil, err
	}
	e.banco.MarcarEnviado(context.Background(), pv.id, resp.ID, resp.Timestamp)
	// O que sai pela ponte entra em `mensagens` na hora, e não quando (ou se) o
	// eco voltar. Sem isto `ultima_interacao` continua dizendo que a última
	// palavra foi do cliente logo depois de o corretor responder, e
	// `retomar-contato` lista quem acabou de ser atendido.
	gravarUma(context.Background(), e.banco, resp.ID, pv.conversa.String(),
		"", pv.nome, true, resp.Timestamp, msg)
	return map[string]any{"id": resp.ID, "em": resp.Timestamp.Unix(),
		"para": pv.nome, "conversa": pv.conversa.String()}, nil
}

func (e *Elo) estado(r *http.Request) (any, error) {
	total, distintos, _, ultimo, err := e.banco.EnviosNaJanela(r.Context(), time.Now().Add(-time.Hour), "")
	if err != nil {
		return nil, err
	}
	var quando int64
	if !ultimo.IsZero() {
		quando = ultimo.Unix()
	}
	return map[string]any{
		"conectado":   e.cli.IsConnected() && e.cli.IsLoggedIn(),
		"envios_hora": total, "conversas_hora": distintos,
		"teto_conversas": distintosHora, "ultimo_envio": quando,
	}, nil
}

// -- as regras -------------------------------------------------------------

// Uma conversa, e uma pessoa. Lista de transmissão e status são exatamente as
// formas que a Meta usa para reconhecer disparo; grupo entrega a mensagem a
// gente que não pediu para falar com o corretor.
func destino(s string) (types.JID, error) {
	jid, err := types.ParseJID(strings.TrimSpace(s))
	if err != nil {
		return jid, fmt.Errorf("jid inválido: %w (use o que veio de listar_conversas)", err)
	}
	switch jid.Server {
	case types.DefaultUserServer, types.HiddenUserServer:
		return jid.ToNonAD(), nil
	case types.GroupServer:
		return jid, fmt.Errorf("esta ponte não envia para grupo")
	case types.BroadcastServer:
		return jid, fmt.Errorf("esta ponte não envia para lista de transmissão nem para status")
	case types.NewsletterServer:
		return jid, fmt.Errorf("esta ponte não envia para canal")
	}
	return jid, fmt.Errorf("destino não reconhecido: %q", jid.Server)
}

func (e *Elo) cabeNaJanela(ctx context.Context, jid string) error {
	total, distintos, jaFalou, ultimo, err := e.banco.EnviosNaJanela(ctx, time.Now().Add(-time.Hour), jid)
	if err != nil {
		return err
	}
	if !ultimo.IsZero() {
		if d := time.Since(ultimo); d < espacoMinimo {
			return fmt.Errorf("espere %.0f s — o envio anterior foi agora", (espacoMinimo - d).Seconds())
		}
	}
	// Responder de novo a quem já se respondeu nesta hora é conversa, não lote.
	if !jaFalou && distintos >= distintosHora {
		return fmt.Errorf("já foram %d conversas distintas nesta hora, que é o teto. "+
			"Isto não faz rodízio de mensagem: o resto se manda pelo celular", distintos)
	}
	if total >= tetoHora {
		return fmt.Errorf("já foram %d envios nesta hora, que é o teto", total)
	}
	return nil
}

func ou(s, alternativa string) string {
	if strings.TrimSpace(s) == "" {
		return alternativa
	}
	return s
}

// Curto e legível em voz alta: o corretor pode dizer "manda a 7Q4K".
func codigoPrevia() string {
	const alfabeto = "ACDEFGHJKLMNPQRTUVWXY34679" // sem I, O, S, B, 0, 1, 2, 5, 8
	b := make([]byte, 4)
	rand.Read(b)
	for i := range b {
		b[i] = alfabeto[int(b[i])%len(alfabeto)]
	}
	return string(b)
}

// -- o lado do mcp ---------------------------------------------------------

type EloCliente struct{ base, segredo string }

func AbrirEloCliente(dir string) (*EloCliente, error) {
	b, err := os.ReadFile(filepath.Join(dir, "elo.json"))
	if err != nil {
		// As quatro tools de leitura seguem funcionando sem daemon: elas só
		// precisam do banco. É o ENVIO que fica indisponível.
		return nil, fmt.Errorf("o daemon não está no ar — abra uma janela e rode `whatsapp-reader serve`")
	}
	var e struct {
		Porta   int    `json:"porta"`
		Segredo string `json:"segredo"`
	}
	if err := json.Unmarshal(b, &e); err != nil || e.Porta == 0 {
		return nil, fmt.Errorf("elo.json ilegível — reinicie o `serve`")
	}
	return &EloCliente{base: fmt.Sprintf("http://127.0.0.1:%d", e.Porta), segredo: e.Segredo}, nil
}

func (c *EloCliente) pedir(ctx context.Context, rota string, corpo any) (map[string]any, error) {
	j, _ := json.Marshal(corpo)
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+rota, bytes.NewReader(j))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Elo", c.segredo)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("o daemon não respondeu (elo.json está velho?): %w", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		if m, _ := out["erro"].(string); m != "" {
			return nil, fmt.Errorf("%s", m)
		}
		return nil, fmt.Errorf("o daemon devolveu %s", resp.Status)
	}
	return out, nil
}
