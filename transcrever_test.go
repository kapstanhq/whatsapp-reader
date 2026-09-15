package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

/* O transcritor com um motor de mentira: ele devolve o texto ou o erro que o
   teste mandar e anota o que recebeu. Banco, disco e relógio são os mesmos da
   esteira de download — é o caminho inteiro, do áudio baixado ao texto. */

type motorFalso struct {
	mu           sync.Mutex
	texto        string
	falta        error   // o que Verificar devolve
	erros        []error // um por chamada, em ordem; esgotados, sucesso
	audios       []transcricao.Audio
	pedidos      []transcricao.Pedido
	verificacoes int
}

func (m *motorFalso) Verificar(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verificacoes++
	return m.falta
}

func (m *motorFalso) Transcrever(_ context.Context, a transcricao.Audio, p transcricao.Pedido) (transcricao.Resultado, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audios = append(m.audios, a)
	m.pedidos = append(m.pedidos, p)
	if len(m.erros) > 0 {
		err := m.erros[0]
		m.erros = m.erros[1:]
		return transcricao.Resultado{}, err
	}
	return transcricao.Resultado{Texto: m.texto, Idioma: p.Idioma, Motor: "falso", Modelo: "de-teste", Levou: 1500 * time.Millisecond}, nil
}

func (m *motorFalso) chamadas() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.audios)
}

func cenarioComMotor(t *testing.T, ajuste func(*configEsteira)) (*cenario, *motorFalso) {
	t.Helper()
	m := &motorFalso{texto: "olha, sobre as calhas, amanhã eu passo aí"}
	c := novoCenario(t, func(cfg *configEsteira) {
		cfg.motor = m
		if ajuste != nil {
			ajuste(cfg)
		}
	})
	return c, m
}

// Chega e baixa: a transcrição fica pendente, pronta para o transcritor.
func (c *cenario) baixado(t *testing.T, id string, sha byte) string {
	t.Helper()
	caminho, conteudo := "/v/"+id, []byte("OggS "+id)
	c.wa.conteudo[caminho] = conteudo
	c.chega(t, id, notaDeVoz(sha, caminho, uint64(len(conteudo))))
	if !c.e.baixarUma(context.Background()) || c.midia(t, id).estado != midiaBaixada {
		t.Fatalf("%s não baixou: %+v", id, c.midia(t, id))
	}
	return filepath.Join(c.dir, filepath.FromSlash(c.midia(t, id).arquivo))
}

type estadoTranscricao struct {
	estado, texto, motor, modelo, erro string
	tentativas                         int
	proxima, levou                     int64
}

func (c *cenario) transcrita(t *testing.T, id string) estadoTranscricao {
	t.Helper()
	var s estadoTranscricao
	if err := c.b.db.QueryRow(`
		SELECT estado, COALESCE(texto, ''), COALESCE(motor, ''), COALESCE(modelo, ''), COALESCE(erro, ''),
		       tentativas, proxima_em, COALESCE(levou_ms, 0)
		  FROM transcricoes WHERE mensagem = ?`, id).
		Scan(&s.estado, &s.texto, &s.motor, &s.modelo, &s.erro, &s.tentativas, &s.proxima, &s.levou); err != nil {
		t.Fatalf("transcrição %s: %v", id, err)
	}
	return s
}

func (c *cenario) problema() string {
	p, _ := c.b.LerEstado(context.Background(), "transcricao_problema")
	return p
}

func TestTranscreveOQueBaixou(t *testing.T) {
	c, m := cenarioComMotor(t, nil)
	ctx := context.Background()
	os.WriteFile(filepath.Join(c.dir, arquivoVocabulario), []byte("seu João\ncalhas\n"), 0o600)
	arquivo := c.baixado(t, "A1", 0xab)

	if !c.e.transcreverUma(ctx) {
		t.Fatal("havia transcrição pendente e o transcritor não pegou")
	}
	s := c.transcrita(t, "A1")
	if s.estado != "feita" || s.texto != m.texto || s.motor != "falso" || s.modelo != "de-teste" || s.levou != 1500 || s.erro != "" {
		t.Fatalf("transcrição = %+v", s)
	}
	if a := m.audios[0]; a.Caminho != arquivo || a.Duracao != 42*time.Second || !strings.HasPrefix(a.Mime, "audio/ogg") {
		t.Errorf("áudio entregue ao motor = %+v", a)
	}
	if p := m.pedidos[0]; p.Idioma != "pt" || p.Dica != "seu João, calhas" {
		t.Errorf("pedido = %+v", p)
	}
	if c.e.transcreverUma(ctx) {
		t.Error("a fila de transcrição devia estar vazia")
	}
}

func TestTranscricaoDeEncaminhadoReaproveitaOTexto(t *testing.T) {
	c, m := cenarioComMotor(t, nil)
	ctx := context.Background()
	c.baixado(t, "A1", 0xab)
	c.rel.andar(time.Minute)
	c.baixado(t, "A2", 0xab) // o mesmo conteúdo, noutra mensagem

	c.e.transcreverUma(ctx)
	c.e.transcreverUma(ctx)
	if n := m.chamadas(); n != 1 {
		t.Errorf("o mesmo áudio foi transcrito %d vezes", n)
	}
	for _, id := range []string{"A1", "A2"} {
		if s := c.transcrita(t, id); s.estado != "feita" || s.texto != m.texto {
			t.Errorf("%s = %+v", id, s)
		}
	}
}

func TestTranscricaoPassageiraEsperaEDesisteNoMaximo(t *testing.T) {
	c, m := cenarioComMotor(t, func(cfg *configEsteira) { cfg.maxTranscricoes = 2 })
	ctx := context.Background()
	m.erros = []error{errors.New("whisper.cpp: failed to process audio"), errors.New("whisper.cpp: failed to process audio")}
	c.baixado(t, "A1", 0xab)

	c.e.transcreverUma(ctx)
	s := c.transcrita(t, "A1")
	if s.estado != "pendente" || s.tentativas != 1 || s.proxima <= c.rel.agora().Unix() || s.erro == "" {
		t.Fatalf("erro passageiro devia adiar: %+v", s)
	}
	if c.e.transcreverUma(ctx) {
		t.Error("dentro da espera a transcrição não pode ser reivindicada")
	}
	c.rel.andar(time.Hour)
	c.e.transcreverUma(ctx)
	if s := c.transcrita(t, "A1"); s.estado != "falhou" || !strings.Contains(s.erro, "desisti depois de 2") {
		t.Errorf("devia desistir na segunda tentativa: %+v", s)
	}
}

func TestTranscricaoNoLimiteDoServicoNaoGastaTentativa(t *testing.T) {
	c, m := cenarioComMotor(t, nil)
	m.erros = []error{fmt.Errorf("openai: %w", &transcricao.ErroLimite{Depois: 2 * time.Hour})}
	c.baixado(t, "A1", 0xab)

	c.e.transcreverUma(context.Background())
	if s := c.transcrita(t, "A1"); s.estado != "pendente" || s.tentativas != 0 || s.proxima < c.rel.agora().Add(2*time.Hour).Unix() {
		t.Errorf("limite do serviço devia esperar o que ele pediu, sem gastar tentativa: %+v", s)
	}
}

func TestTranscricaoDefinitivaNaoRepete(t *testing.T) {
	c, m := cenarioComMotor(t, nil)
	ctx := context.Background()
	m.erros = []error{fmt.Errorf("ffmpeg: não converteu: %w", transcricao.ErrAudioInvalido)}
	c.baixado(t, "A1", 0xab)

	c.e.transcreverUma(ctx)
	c.rel.andar(7 * time.Hour)
	c.e.transcreverUma(ctx)
	if s := c.transcrita(t, "A1"); s.estado != "falhou" || m.chamadas() != 1 {
		t.Errorf("áudio inválido não se repete: %+v, %d chamadas", s, m.chamadas())
	}
}

func TestTranscricaoSemMotorPausaEVoltaSozinha(t *testing.T) {
	c, m := cenarioComMotor(t, nil)
	ctx := context.Background()
	m.falta = fmt.Errorf(`whisper.cpp: exec: "whisper-cli": executable file not found in %%PATH%%: %w`, transcricao.ErrMotorAusente)
	c.baixado(t, "A1", 0xab)

	if c.e.transcreverUma(ctx) {
		t.Fatal("sem whisper-cli não se reivindica nada")
	}
	if s := c.transcrita(t, "A1"); s.estado != "pendente" || s.tentativas != 0 {
		t.Errorf("a fila não pode andar sem motor: %+v", s)
	}
	if !strings.Contains(c.problema(), "whisper-cli") {
		t.Errorf("o problema devia ir para o estado: %q", c.problema())
	}

	m.falta = nil // instalou, com o daemon no ar
	c.e.transcreverUma(ctx)
	if m.verificacoes != 1 {
		t.Errorf("conferiu o motor %d vezes antes de a conferência vencer", m.verificacoes)
	}
	c.rel.andar(5 * time.Minute)
	if !c.e.transcreverUma(ctx) || c.transcrita(t, "A1").estado != "feita" {
		t.Fatalf("depois de instalar devia voltar sozinho: %+v", c.transcrita(t, "A1"))
	}
	if p := c.problema(); p != "" {
		t.Errorf("o problema resolvido devia sair do estado: %q", p)
	}
}

func TestTranscricaoComModeloQueNaoCarregaDevolveATentativaEPausa(t *testing.T) {
	c, m := cenarioComMotor(t, nil)
	ctx := context.Background()
	m.erros = []error{fmt.Errorf("whisper.cpp: error: failed to initialize whisper context: %w", transcricao.ErrMotorAusente)}
	c.baixado(t, "A1", 0xab)

	c.e.transcreverUma(ctx)
	if s := c.transcrita(t, "A1"); s.estado != "pendente" || s.tentativas != 0 {
		t.Errorf("problema de instalação não é culpa do áudio: %+v", s)
	}
	if !strings.Contains(c.problema(), "failed to initialize") {
		t.Errorf("problema = %q", c.problema())
	}
	if c.e.transcreverUma(ctx) {
		t.Error("devia pausar até a próxima conferência")
	}
}

func TestTranscricaoCanceladaDevolveALinha(t *testing.T) {
	c, m := cenarioComMotor(t, nil)
	m.erros = []error{fmt.Errorf("whisper.cpp: %w", context.Canceled)}
	c.baixado(t, "A1", 0xab)

	c.e.transcreverUma(context.Background())
	if s := c.transcrita(t, "A1"); s.estado != "pendente" || s.tentativas != 0 || s.proxima != 0 {
		t.Errorf("cancelada devia voltar intacta: %+v", s)
	}
}

func TestTranscricaoComArquivoSumidoBaixaDeNovo(t *testing.T) {
	c, m := cenarioComMotor(t, nil)
	ctx := context.Background()
	os.Remove(c.baixado(t, "A1", 0xab))

	if !c.e.transcreverUma(ctx) {
		t.Fatal("a transcrição pendente não foi reivindicada")
	}
	if md := c.midia(t, "A1"); md.estado != midiaPendente || md.arquivo != "" {
		t.Fatalf("sem o arquivo, a mídia devia voltar para o download: %+v", md)
	}
	if s := c.transcrita(t, "A1"); s.estado != "pendente" || s.tentativas != 0 || m.chamadas() != 0 {
		t.Errorf("transcrição = %+v, %d chamadas", s, m.chamadas())
	}
	c.e.baixarUma(ctx)
	c.e.transcreverUma(ctx)
	if s := c.transcrita(t, "A1"); s.estado != "feita" {
		t.Errorf("baixado de novo, devia transcrever: %+v", s)
	}
}

func TestTranscricaoDesligadaNaoReivindica(t *testing.T) {
	c := novoCenario(t, nil) // sem motor
	c.baixado(t, "A1", 0xab)
	if c.e.transcreverUma(context.Background()) {
		t.Error("com a transcrição desligada nada é reivindicado")
	}
	if s := c.transcrita(t, "A1"); s.estado != "pendente" {
		t.Errorf("a transcrição devia esperar o motor ser ligado: %+v", s)
	}
}

func TestEsteiraComWorkersBaixaETranscreveSozinha(t *testing.T) {
	c, m := cenarioComMotor(t, func(cfg *configEsteira) { cfg.baixadores = 2; cfg.intervalo = time.Hour })
	c.e.agora = time.Now
	c.g.agora = time.Now
	c.wa.conteudo["/v/calhas"] = []byte("OggS")
	if err := c.e.iniciar(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.e.Fechar()

	c.chega(t, "A1", notaDeVoz(0xab, "/v/calhas", 4))
	estado := func() string {
		var s string
		c.b.db.QueryRow(`SELECT estado FROM transcricoes WHERE mensagem = 'A1'`).Scan(&s)
		return s
	}
	prazo := time.Now().Add(5 * time.Second)
	for estado() != "feita" {
		if time.Now().After(prazo) {
			t.Fatalf("o transcritor não acordou: mídia %+v, transcrição %q", c.midia(t, "A1"), estado())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := m.chamadas(); n != 1 {
		t.Errorf("%d transcrições para um áudio", n)
	}
}
