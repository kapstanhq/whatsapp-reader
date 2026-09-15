package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/gcmutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"
	"google.golang.org/protobuf/proto"
)

/* A esteira sem conta pareada: o WhatsApp é um falso que baixa por DirectPath,
   devolve o erro que o teste mandar e anota os pedidos ao celular. O banco e o
   disco são de verdade — são eles que precisam sobreviver a queda, duplicata
   e cancelamento. */

type waFalso struct {
	mu        sync.Mutex
	conectado bool
	conteudo  map[string][]byte
	erros     map[string]error
	baixados  int
	pedidos   []types.MessageInfo
}

func novoWA() *waFalso {
	return &waFalso{conectado: true, conteudo: map[string][]byte{}, erros: map[string]error{}}
}

func (w *waFalso) DownloadToFile(_ context.Context, msg whatsmeow.DownloadableMessage, f whatsmeow.File) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.baixados++
	if err := w.erros[msg.GetDirectPath()]; err != nil {
		return err
	}
	f.Write(w.conteudo[msg.GetDirectPath()])
	return nil
}

func (w *waFalso) SendMediaRetryReceipt(_ context.Context, info *types.MessageInfo, _ []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pedidos = append(w.pedidos, *info)
	return nil
}

func (w *waFalso) IsConnected() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.conectado }
func (w *waFalso) IsLoggedIn() bool  { return true }

type relogio struct {
	mu sync.Mutex
	t  time.Time
}

func (r *relogio) agora() time.Time      { r.mu.Lock(); defer r.mu.Unlock(); return r.t }
func (r *relogio) andar(d time.Duration) { r.mu.Lock(); r.t = r.t.Add(d); r.mu.Unlock() }

// Uma nota de voz com sha e caminho próprios, para duas não colidirem.
func notaDeVoz(sha byte, caminho string, tamanho uint64) *waE2E.Message {
	h := make([]byte, 32)
	h[0], h[31] = sha, sha
	chave := make([]byte, 32)
	chave[0] = sha ^ 0x5a
	return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		Mimetype: proto.String("audio/ogg; codecs=opus"), Seconds: proto.Uint32(42), PTT: proto.Bool(true),
		FileLength: proto.Uint64(tamanho), FileSHA256: h, FileEncSHA256: h, MediaKey: chave,
		DirectPath: proto.String(caminho),
	}}
}

type cenario struct {
	b   *Banco
	dir string
	wa  *waFalso
	rel *relogio
	e   *Esteira
	g   *gravador
}

func novoCenario(t *testing.T, ajuste func(*configEsteira)) *cenario {
	t.Helper()
	dir := t.TempDir()
	b, err := AbrirBanco(filepath.Join(dir, "mensagens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Fechar() })
	c := &cenario{b: b, dir: dir, wa: novoWA(), rel: &relogio{t: time.Date(2026, 9, 15, 14, 0, 0, 0, time.Local)}}
	cfg := configEsteiraPadrao(7)
	cfg.baixadores = 0 // os testes chamam baixarUma na mão; o de workers liga os seus
	if ajuste != nil {
		ajuste(&cfg)
	}
	c.e = novaEsteira(dir, b, c.wa, cfg, c.rel.agora)
	c.g = &gravador{banco: b, dias: 7, agora: c.rel.agora, acordar: c.e.Acordar}
	return c
}

func (c *cenario) chega(t *testing.T, id string, msg *waE2E.Message) {
	t.Helper()
	c.g.gravar(context.Background(), joaoJID, eventoDe(t, joaoJID, id, c.rel.agora(), msg), origemAoVivo)
}

type estadoMidia struct {
	estado, arquivo, erro string
	tentativas            int
	proxima               int64
	anexo                 []byte
}

func (c *cenario) midia(t *testing.T, id string) estadoMidia {
	t.Helper()
	var s estadoMidia
	if err := c.b.db.QueryRow(`
		SELECT estado, COALESCE(arquivo,''), COALESCE(erro,''), tentativas, proxima_em, anexo
		  FROM midias WHERE mensagem = ?`, id).
		Scan(&s.estado, &s.arquivo, &s.erro, &s.tentativas, &s.proxima, &s.anexo); err != nil {
		t.Fatalf("mídia %s: %v", id, err)
	}
	return s
}

func (c *cenario) transcricaoPendente(t *testing.T, id string) bool {
	t.Helper()
	var estado string
	c.b.db.QueryRow(`SELECT estado FROM transcricoes WHERE mensagem = ?`, id).Scan(&estado)
	return estado == "pendente"
}

func TestEsteiraBaixaOAudioEDeixaATranscricaoNaFila(t *testing.T) {
	c := novoCenario(t, nil)
	ctx := context.Background()
	c.wa.conteudo["/v/calhas"] = []byte("OggS nota do seu João")
	c.chega(t, "A1", notaDeVoz(0xab, "/v/calhas", 22))

	if !c.e.baixarUma(ctx) {
		t.Fatal("havia um áudio pendente e a esteira não pegou")
	}
	s := c.midia(t, "A1")
	if s.estado != midiaBaixada || s.erro != "" {
		t.Fatalf("estado = %+v", s)
	}
	sha := hex.EncodeToString(notaDeVoz(0xab, "", 0).GetAudioMessage().GetFileSHA256())
	if s.arquivo != "midia/ab/"+sha+".ogg" {
		t.Errorf("arquivo = %q", s.arquivo)
	}
	if got, err := os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(s.arquivo))); err != nil || string(got) != "OggS nota do seu João" {
		t.Errorf("conteúdo no disco = %q, %v", got, err)
	}
	if !c.transcricaoPendente(t, "A1") {
		t.Error("o áudio baixado devia entrar na fila de transcrição")
	}
	if c.e.baixarUma(ctx) {
		t.Error("a fila devia estar vazia")
	}
}

func TestEsteiraEsperaAConexao(t *testing.T) {
	c := novoCenario(t, nil)
	c.wa.conectado = false
	c.chega(t, "A1", notaDeVoz(0xab, "/v/calhas", 0))
	if c.e.baixarUma(context.Background()) {
		t.Error("desconectada, a esteira não pode reivindicar nada")
	}
	if s := c.midia(t, "A1"); s.estado != midiaPendente || s.tentativas != 0 {
		t.Errorf("sem conexão a linha não pode mudar: %+v", s)
	}
}

// A resposta do celular cifrada como o whatsmeow cifra.
func respostaDoCelularCifrada(t *testing.T, chave []byte, id string, n *waMmsRetry.MediaRetryNotification) *events.MediaRetry {
	t.Helper()
	plano, _ := proto.Marshal(n)
	iv := make([]byte, 12)
	rand.Read(iv)
	cifrado, err := gcmutil.Encrypt(hkdfutil.SHA256(chave, nil, []byte("WhatsApp Media Retry Notification"), 32), iv, plano, []byte(id))
	if err != nil {
		t.Fatal(err)
	}
	return &events.MediaRetry{Ciphertext: cifrado, IV: iv, MessageID: types.MessageID(id)}
}

func TestEsteiraLinkVencidoPedeAoCelularERetomaComAResposta(t *testing.T) {
	c := novoCenario(t, nil)
	ctx := context.Background()
	msg := notaDeVoz(0xab, "/v/velho", 0)
	c.wa.erros["/v/velho"] = whatsmeow.ErrMediaDownloadFailedWith404
	c.wa.conteudo["/v/novo"] = []byte("OggS reenviado")
	c.chega(t, "A1", msg)

	c.e.baixarUma(ctx)
	if s := c.midia(t, "A1"); s.estado != midiaPedida {
		t.Fatalf("link vencido devia virar pedido ao celular, ficou %+v", s)
	}
	if len(c.wa.pedidos) != 1 || c.wa.pedidos[0].ID != "A1" || c.wa.pedidos[0].Chat.String() != joaoJID {
		t.Fatalf("pedido ao celular = %+v", c.wa.pedidos)
	}

	chave := msg.GetAudioMessage().GetMediaKey()
	sucesso := &waMmsRetry.MediaRetryNotification{DirectPath: proto.String("/v/novo"),
		Result: waMmsRetry.MediaRetryNotification_SUCCESS.Enum()}
	// Resposta de outro anexo com o mesmo ID não pode mexer na linha.
	c.e.RespostaDoCelular(ctx, respostaDoCelularCifrada(t, make([]byte, 32), "A1", sucesso))
	if s := c.midia(t, "A1"); s.estado != midiaPedida {
		t.Errorf("resposta que não abre com a chave mexeu na linha: %+v", s)
	}

	c.e.RespostaDoCelular(ctx, respostaDoCelularCifrada(t, chave, "A1", sucesso))
	if s := c.midia(t, "A1"); s.estado != midiaPendente || s.proxima != 0 {
		t.Fatalf("a resposta devia devolver a mídia à fila, ficou %+v", s)
	}
	if !c.e.baixarUma(ctx) {
		t.Fatal("a mídia com caminho novo não foi reivindicada")
	}
	if s := c.midia(t, "A1"); s.estado != midiaBaixada {
		t.Errorf("com o caminho novo devia baixar, ficou %+v", s)
	}
}

func TestEsteiraCelularSemOArquivo(t *testing.T) {
	c := novoCenario(t, nil)
	ctx := context.Background()
	msg := notaDeVoz(0xab, "/v/velho", 0)
	c.wa.erros["/v/velho"] = whatsmeow.ErrMediaDownloadFailedWith410
	c.chega(t, "A1", msg)
	c.e.baixarUma(ctx)

	nada := &waMmsRetry.MediaRetryNotification{Result: waMmsRetry.MediaRetryNotification_NOT_FOUND.Enum()}
	c.e.RespostaDoCelular(ctx, respostaDoCelularCifrada(t, msg.GetAudioMessage().GetMediaKey(), "A1", nada))
	if s := c.midia(t, "A1"); s.estado != midiaIndisponivel {
		t.Errorf("celular sem o arquivo devia deixar indisponível, ficou %+v", s)
	}
}

func TestEsteiraDesisteDoQueNaoConfere(t *testing.T) {
	c := novoCenario(t, nil)
	c.wa.erros["/v/x"] = whatsmeow.ErrInvalidMediaHMAC
	c.chega(t, "A1", notaDeVoz(0xab, "/v/x", 0))
	c.e.baixarUma(context.Background())
	if s := c.midia(t, "A1"); s.estado != midiaFalhou || s.erro == "" {
		t.Errorf("HMAC que não confere é definitivo, ficou %+v", s)
	}
}

func TestEsteiraErroDeRedeTentaMaisTardeEDesisteNoMaximo(t *testing.T) {
	c := novoCenario(t, func(cfg *configEsteira) { cfg.maxTentativas = 3 })
	ctx := context.Background()
	c.wa.erros["/v/x"] = errors.New("connection reset by peer")
	c.chega(t, "A1", notaDeVoz(0xab, "/v/x", 0))

	c.e.baixarUma(ctx)
	s := c.midia(t, "A1")
	if s.estado != midiaPendente || s.tentativas != 1 || s.proxima <= c.rel.agora().Unix() {
		t.Fatalf("erro de rede devia adiar: %+v", s)
	}
	if c.e.baixarUma(ctx) {
		t.Error("dentro da espera a mídia não pode ser reivindicada")
	}
	for i := 0; i < 5 && c.midia(t, "A1").estado != midiaFalhou; i++ {
		c.rel.andar(7 * time.Hour)
		c.e.baixarUma(ctx)
	}
	if s := c.midia(t, "A1"); s.estado != midiaFalhou || s.tentativas != 3 {
		t.Errorf("devia desistir na terceira tentativa, ficou %+v", s)
	}
}

func TestEsteiraTetoDePedidosPorHoraNaoGastaTentativa(t *testing.T) {
	c := novoCenario(t, func(cfg *configEsteira) { cfg.pedidosPorHora = 1 })
	ctx := context.Background()
	c.wa.erros["/v/a"] = whatsmeow.ErrMediaDownloadFailedWith404
	c.wa.erros["/v/b"] = whatsmeow.ErrMediaDownloadFailedWith404
	c.chega(t, "A1", notaDeVoz(0x01, "/v/a", 0))
	c.rel.andar(time.Minute)
	c.chega(t, "A2", notaDeVoz(0x02, "/v/b", 0))

	c.e.baixarUma(ctx) // A2, a mais nova: pedida
	c.e.baixarUma(ctx) // A1: teto
	if s := c.midia(t, "A2"); s.estado != midiaPedida {
		t.Errorf("A2 devia ter sido pedida: %+v", s)
	}
	s := c.midia(t, "A1")
	if s.estado != midiaPendente || s.tentativas != 0 || s.proxima < c.rel.agora().Add(59*time.Minute).Unix() {
		t.Errorf("acima do teto devia esperar uma hora sem gastar tentativa: %+v", s)
	}
	if len(c.wa.pedidos) != 1 {
		t.Errorf("foram %d pedidos, o teto é 1", len(c.wa.pedidos))
	}
}

type waCancelado struct{ *waFalso }

func (w waCancelado) DownloadToFile(context.Context, whatsmeow.DownloadableMessage, whatsmeow.File) error {
	return context.Canceled
}

// O Ctrl+C no meio do download: o whatsmeow devolve context.Canceled. A linha
// volta para a fila como estava — sem tentativa gasta e sem espera.
func TestEsteiraCanceladaDevolveALinhaSemGastarTentativa(t *testing.T) {
	c := novoCenario(t, nil)
	c.chega(t, "A1", notaDeVoz(0xab, "/v/x", 0))
	e := novaEsteira(c.dir, c.b, waCancelado{c.wa}, c.e.cfg, c.rel.agora)
	if !e.baixarUma(context.Background()) {
		t.Fatal("a mídia pendente não foi reivindicada")
	}
	s := c.midia(t, "A1")
	if s.estado != midiaPendente || s.tentativas != 0 || s.proxima != 0 {
		t.Errorf("cancelado devia voltar para a fila intacto: %+v", s)
	}
}

func TestEsteiraAudioEncaminhadoNaoBaixaDuasVezes(t *testing.T) {
	c := novoCenario(t, nil)
	ctx := context.Background()
	ogg := []byte("OggS mesmo áudio")
	c.wa.conteudo["/v/um"], c.wa.conteudo["/v/dois"] = ogg, ogg
	c.chega(t, "A1", notaDeVoz(0xab, "/v/um", uint64(len(ogg))))
	c.rel.andar(time.Minute)
	c.chega(t, "A2", notaDeVoz(0xab, "/v/dois", uint64(len(ogg)))) // encaminhado: mesmo conteúdo, outro caminho

	c.e.baixarUma(ctx)
	c.e.baixarUma(ctx)
	if c.wa.baixados != 1 {
		t.Errorf("o mesmo conteúdo foi baixado %d vezes", c.wa.baixados)
	}
	if a1, a2 := c.midia(t, "A1"), c.midia(t, "A2"); a1.estado != midiaBaixada || a2.estado != midiaBaixada || a1.arquivo != a2.arquivo {
		t.Errorf("as duas deviam apontar para o mesmo arquivo: %+v / %+v", a1, a2)
	}
}

func TestEsteiraRecuperaODepoisDaQueda(t *testing.T) {
	c := novoCenario(t, nil)
	ctx := context.Background()
	c.chega(t, "A1", notaDeVoz(0x01, "/v/a", 0))
	c.chega(t, "A2", notaDeVoz(0x02, "/v/b", 0))
	c.chega(t, "A3", notaDeVoz(0x03, "/v/c", 0))
	agora := c.rel.agora()
	c.b.db.Exec(`UPDATE midias SET estado = 'baixando' WHERE mensagem = 'A1'`)
	c.b.db.Exec(`UPDATE midias SET estado = 'pedida', pedida_em = ? WHERE mensagem = 'A2'`, agora.Add(-25*time.Hour).Unix())
	c.b.db.Exec(`UPDATE midias SET estado = 'guardada', erro = 'antigo' WHERE mensagem = 'A3'`)
	parcial := filepath.Join(c.dir, "midia", "ab", "lixo.ogg.parcial")
	os.MkdirAll(filepath.Dir(parcial), 0o755)
	os.WriteFile(parcial, []byte("pela metade"), 0o600)

	if err := c.e.recuperar(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"A1", "A2", "A3"} {
		if s := c.midia(t, id); s.estado != midiaPendente {
			t.Errorf("%s devia voltar para a fila, ficou %+v", id, s)
		}
	}
	naoExisteArquivo(t, parcial)
}

func naoExisteArquivo(t *testing.T, caminho string) {
	t.Helper()
	if _, err := os.Stat(caminho); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s devia ter sido apagado", caminho)
	}
}

func TestEsteiraComWorkersAcordaEBaixaSozinha(t *testing.T) {
	c := novoCenario(t, func(cfg *configEsteira) { cfg.baixadores = 2; cfg.intervalo = time.Hour })
	c.e.agora = time.Now
	c.g.agora = time.Now
	c.wa.conteudo["/v/calhas"] = []byte("OggS")
	if err := c.e.iniciar(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.e.Fechar()

	c.chega(t, "A1", notaDeVoz(0xab, "/v/calhas", 4))
	prazo := time.Now().Add(5 * time.Second)
	for c.midia(t, "A1").estado != midiaBaixada {
		if time.Now().After(prazo) {
			t.Fatalf("a campainha não acordou ninguém: %+v", c.midia(t, "A1"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	inicio := time.Now()
	c.e.Fechar()
	if d := time.Since(inicio); d > 2*time.Second {
		t.Errorf("Fechar demorou %s com os workers ociosos", d)
	}
}

func TestAtrasoDobraAteOTeto(t *testing.T) {
	for tentativa, esperado := range map[int]time.Duration{1: 2 * time.Minute, 2: 4 * time.Minute, 4: 16 * time.Minute, 20: 6 * time.Hour} {
		d := atraso(2*time.Minute, tentativa)
		if d < esperado*8/10 || d > esperado*12/10 {
			t.Errorf("atraso(tentativa %d) = %s, esperava %s ±20%%", tentativa, d, esperado)
		}
	}
}
