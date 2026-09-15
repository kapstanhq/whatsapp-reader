package openai

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

const chaveDeTeste = "gsk_segredo_de_teste"

type recebido struct {
	caminho, autorizacao string
	campos               map[string]string
	nomeDoArquivo        string
	arquivo              string
}

type servidor struct {
	*httptest.Server
	mu      sync.Mutex
	pedidos []recebido
}

func novoServidor(t *testing.T, responder http.HandlerFunc) *servidor {
	t.Helper()
	s := &servidor{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recebido{caminho: r.URL.Path, autorizacao: r.Header.Get("Authorization"), campos: map[string]string{}}
		if err := r.ParseMultipartForm(32 << 20); err == nil {
			for campo, valores := range r.MultipartForm.Value {
				rec.campos[campo] = valores[0]
			}
			if arquivos := r.MultipartForm.File["file"]; len(arquivos) > 0 {
				rec.nomeDoArquivo = arquivos[0].Filename
				if f, err := arquivos[0].Open(); err == nil {
					dados, _ := io.ReadAll(f)
					f.Close()
					rec.arquivo = string(dados)
				}
			}
		}
		s.mu.Lock()
		s.pedidos = append(s.pedidos, rec)
		s.mu.Unlock()
		responder(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *servidor) recebidos() []recebido {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recebido(nil), s.pedidos...)
}

func responde(status int, corpo string, cabecalhos ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i+1 < len(cabecalhos); i += 2 {
			w.Header().Set(cabecalhos[i], cabecalhos[i+1])
		}
		w.WriteHeader(status)
		io.WriteString(w, corpo)
	}
}

const conteudoDaNota = "OggS nota do seu João"

// O nome no disco é o hash do conteúdo, como na esteira.
func nota(t *testing.T) string {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "ab12cd34.ogg")
	if err := os.WriteFile(caminho, []byte(conteudoDaNota), 0o600); err != nil {
		t.Fatal(err)
	}
	return caminho
}

func TestTranscreveComOFormularioDaOpenAI(t *testing.T) {
	s := novoServidor(t, responde(200, `{"text": " Oi, aqui é o João. ", "x_groq": {"id": "req_1"}}`))
	m := Novo(Config{URL: s.URL + "/openai/v1/", Chave: chaveDeTeste, Modelo: "whisper-large-v3-turbo"})

	r, err := m.Transcrever(context.Background(),
		transcricao.Audio{Caminho: nota(t), Mime: "audio/ogg; codecs=opus"},
		transcricao.Pedido{Idioma: "pt", Dica: "calhas, seu João"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Texto != "Oi, aqui é o João." || r.Idioma != "pt" || r.Motor != "openai" || r.Modelo != "whisper-large-v3-turbo" || r.Levou <= 0 {
		t.Errorf("resultado = %+v", r)
	}

	pedidos := s.recebidos()
	if len(pedidos) != 1 {
		t.Fatalf("%d requisições", len(pedidos))
	}
	p := pedidos[0]
	if p.caminho != "/openai/v1/audio/transcriptions" {
		t.Errorf("caminho = %q", p.caminho)
	}
	if p.autorizacao != "Bearer "+chaveDeTeste {
		t.Errorf("Authorization = %q", p.autorizacao)
	}
	if p.nomeDoArquivo != "audio.ogg" || p.arquivo != conteudoDaNota {
		t.Errorf("arquivo = %q (%q): o nome no disco não pode sair, o conteúdo sim", p.nomeDoArquivo, p.arquivo)
	}
	for campo, valor := range map[string]string{
		"model": "whisper-large-v3-turbo", "language": "pt", "prompt": "calhas, seu João", "response_format": "json",
	} {
		if p.campos[campo] != valor {
			t.Errorf("campo %s = %q, queria %q", campo, p.campos[campo], valor)
		}
	}
}

func TestSemChaveSemIdiomaSemDicaNaoMandaOsCampos(t *testing.T) {
	s := novoServidor(t, responde(200, `{"text": ""}`))
	m := Novo(Config{URL: s.URL + "/v1", Modelo: "Systran/faster-whisper-small"})
	r, err := m.Transcrever(context.Background(), transcricao.Audio{Caminho: nota(t)}, transcricao.Pedido{})
	if err != nil || r.Texto != "" {
		t.Fatalf("texto vazio é sucesso: %+v, %v", r, err)
	}
	p := s.recebidos()[0]
	if p.autorizacao != "" {
		t.Errorf("sem chave não devia haver Authorization: %q", p.autorizacao)
	}
	for _, campo := range []string{"language", "prompt"} {
		if _, ok := p.campos[campo]; ok {
			t.Errorf("campo %s foi enviado vazio", campo)
		}
	}
}

func TestCampoDeIdiomaEEnderecoCompleto(t *testing.T) {
	s := novoServidor(t, responde(200, `{"text": "ok"}`))
	m := Novo(Config{URL: s.URL + "/v1/audio/transcriptions", Modelo: "m", CampoIdioma: "languages"})
	if _, err := m.Transcrever(context.Background(), transcricao.Audio{Caminho: nota(t)}, transcricao.Pedido{Idioma: "pt"}); err != nil {
		t.Fatal(err)
	}
	p := s.recebidos()[0]
	if p.caminho != "/v1/audio/transcriptions" {
		t.Errorf("caminho = %q: o endereço completo não pode ser repetido", p.caminho)
	}
	if p.campos["languages"] != "pt" {
		t.Errorf("campos = %v", p.campos)
	}
}

func TestExtensao(t *testing.T) {
	casos := []struct {
		audio transcricao.Audio
		quer  string
	}{
		{transcricao.Audio{Caminho: "midia/ab/ab12.OGG"}, ".ogg"},
		{transcricao.Audio{Caminho: "sem-extensao", Mime: "audio/mpeg"}, ".mp3"},
		{transcricao.Audio{Caminho: "sem-extensao", Mime: "audio/mp4; codecs=mp4a.40.2"}, ".m4a"},
		{transcricao.Audio{Caminho: "sem-extensao", Mime: "audio/ogg; codecs=opus"}, ".ogg"},
		{transcricao.Audio{Caminho: "sem-extensao"}, ".ogg"},
	}
	for _, c := range casos {
		if got := extensao(c.audio); got != c.quer {
			t.Errorf("extensao(%+v) = %q, queria %q", c.audio, got, c.quer)
		}
	}
}

func TestRespostasDeErro(t *testing.T) {
	casos := []struct {
		nome       string
		status     int
		corpo      string
		cabecalhos []string
		sentinela  error // nil: nenhum sentinela, só a classe
		classe     transcricao.Classe
		frase      string
	}{
		{"chave recusada, e o serviço repete a chave", 401,
			`{"error":{"message":"Incorrect API key provided: ` + chaveDeTeste + `.","type":"invalid_request_error","code":"invalid_api_key"}}`, nil,
			transcricao.ErrCredencial, transcricao.Configuracao, "401"},
		{"sem permissão", 403, `{"error":{"message":"Forbidden"}}`, nil,
			transcricao.ErrCredencial, transcricao.Configuracao, "403"},
		{"conta sem crédito", 429,
			`{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`, nil,
			transcricao.ErrCredencial, transcricao.Configuracao, "crédito"},
		{"limite com Retry-After", 429, `{"error":{"message":"Rate limit reached for model whisper-large-v3-turbo"}}`, []string{"Retry-After", "17"},
			transcricao.ErrLimite, transcricao.Passageiro, "Rate limit reached"},
		{"endereço errado", 404, "404 page not found", nil,
			transcricao.ErrMotorAusente, transcricao.Configuracao, "404 page not found"},
		{"modelo inexistente", 400,
			`{"error":{"message":"The model 'whisper-9' does not exist","type":"invalid_request_error","param":"model","code":"model_not_found"}}`, nil,
			transcricao.ErrMotorAusente, transcricao.Configuracao, "whisper-9"},
		{"formato recusado", 400, `{"error":{"message":"Invalid file format.","type":"invalid_request_error"}}`, nil,
			transcricao.ErrAudioInvalido, transcricao.Definitivo, "Invalid file format."},
		{"grande demais para o serviço", 413, "request entity too large", nil,
			transcricao.ErrAudioInvalido, transcricao.Definitivo, "413"},
		{"tipo não suportado", 415, "", nil,
			transcricao.ErrAudioInvalido, transcricao.Definitivo, "415"},
		{"áudio que o servidor não processa", 422, `{"detail":"invalid audio"}`, nil,
			transcricao.ErrAudioInvalido, transcricao.Definitivo, "invalid audio"},
		{"servidor fora", 502, "<html>\n<body>Bad Gateway</body>\n</html>", nil,
			nil, transcricao.Passageiro, "502 <html> <body>Bad Gateway</body> </html>"},
		{"200 sem o campo text", 200, `{"texto": "x"}`, nil,
			nil, transcricao.Passageiro, "sem o campo text"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			s := novoServidor(t, responde(c.status, c.corpo, c.cabecalhos...))
			m := Novo(Config{URL: s.URL + "/v1", Chave: chaveDeTeste, Modelo: "whisper-large-v3-turbo"})
			_, err := m.Transcrever(context.Background(), transcricao.Audio{Caminho: nota(t)}, transcricao.Pedido{Idioma: "pt"})
			if err == nil {
				t.Fatal("devia falhar")
			}
			if c.sentinela != nil && !errors.Is(err, c.sentinela) {
				t.Errorf("erro = %v, queria %v", err, c.sentinela)
			}
			if got := transcricao.Classificar(err); got != c.classe {
				t.Errorf("classe = %s, queria %s (%v)", got, c.classe, err)
			}
			if !strings.Contains(err.Error(), c.frase) {
				t.Errorf("a mensagem devia trazer %q: %v", c.frase, err)
			}
			if strings.Contains(err.Error(), chaveDeTeste) {
				t.Errorf("a chave vazou na mensagem: %v", err)
			}
		})
	}
}

func TestLimiteTrazOTempoPedido(t *testing.T) {
	s := novoServidor(t, responde(429, `{"error":{"message":"slow down"}}`, "Retry-After", "17"))
	m := Novo(Config{URL: s.URL + "/v1", Modelo: "m"})
	_, err := m.Transcrever(context.Background(), transcricao.Audio{Caminho: nota(t)}, transcricao.Pedido{})
	var limite *transcricao.ErroLimite
	if !errors.As(err, &limite) || limite.Depois != 17*time.Second {
		t.Errorf("erro = %v, limite = %+v", err, limite)
	}
}

func TestEsperaPedida(t *testing.T) {
	agora := time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC)
	casos := map[string]time.Duration{
		"":     0,
		"17":   17 * time.Second,
		"0":    0,
		"-3":   0,
		"logo": 0,
		agora.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second,
		agora.Add(-time.Minute).Format(http.TimeFormat):     0,
	}
	for valor, quer := range casos {
		h := http.Header{}
		if valor != "" {
			h.Set("Retry-After", valor)
		}
		if got := esperaPedida(h, agora); got != quer {
			t.Errorf("Retry-After %q = %s, queria %s", valor, got, quer)
		}
	}
}

func TestArquivoGrandeNemEEnviado(t *testing.T) {
	s := novoServidor(t, responde(200, `{"text": "x"}`))
	m := Novo(Config{URL: s.URL + "/v1", Modelo: "m", TamanhoMax: int64(len(conteudoDaNota)) - 1})
	_, err := m.Transcrever(context.Background(), transcricao.Audio{Caminho: nota(t)}, transcricao.Pedido{})
	if !errors.Is(err, transcricao.ErrAudioInvalido) {
		t.Errorf("erro = %v", err)
	}
	if n := len(s.recebidos()); n != 0 {
		t.Errorf("o arquivo grande foi enviado (%d requisições)", n)
	}
}

func TestArquivoSumidoNaoCondenaOAudio(t *testing.T) {
	s := novoServidor(t, responde(200, `{"text": "x"}`))
	m := Novo(Config{URL: s.URL + "/v1", Modelo: "m"})
	_, err := m.Transcrever(context.Background(), transcricao.Audio{Caminho: filepath.Join(t.TempDir(), "sumiu.ogg")}, transcricao.Pedido{})
	if !errors.Is(err, fs.ErrNotExist) || transcricao.Classificar(err) != transcricao.Passageiro {
		t.Errorf("erro = %v", err)
	}
	if n := len(s.recebidos()); n != 0 {
		t.Errorf("%d requisições sem arquivo", n)
	}
}

func TestRedeForaEPassageiro(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	endereco := s.URL
	s.Close()
	m := Novo(Config{URL: endereco + "/v1", Chave: chaveDeTeste, Modelo: "m"})
	_, err := m.Transcrever(context.Background(), transcricao.Audio{Caminho: nota(t)}, transcricao.Pedido{})
	if err == nil || transcricao.Classificar(err) != transcricao.Passageiro {
		t.Errorf("erro = %v, classe = %s", err, transcricao.Classificar(err))
	}
	if err != nil && strings.Contains(err.Error(), chaveDeTeste) {
		t.Errorf("a chave vazou na mensagem: %v", err)
	}
}

func TestCanceladoNoMeioDaRequisicao(t *testing.T) {
	s := novoServidor(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	m := Novo(Config{URL: s.URL + "/v1", Modelo: "m"})
	ctx, cancelar := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancelar)
	_, err := m.Transcrever(ctx, transcricao.Audio{Caminho: nota(t)}, transcricao.Pedido{})
	if !errors.Is(err, context.Canceled) || transcricao.Classificar(err) != transcricao.Cancelado {
		t.Errorf("erro = %v", err)
	}
}

func TestVerificarNaoVaiARede(t *testing.T) {
	s := novoServidor(t, responde(500, ""))
	casos := []struct {
		nome string
		cfg  Config
		ok   bool
	}{
		{"completo", Config{URL: s.URL + "/v1", Modelo: "m"}, true},
		{"sem URL", Config{Modelo: "m"}, false},
		{"sem esquema", Config{URL: "api.groq.com/openai/v1", Modelo: "m"}, false},
		{"esquema errado", Config{URL: "ftp://api.groq.com/openai/v1", Modelo: "m"}, false},
		{"sem modelo", Config{URL: s.URL + "/v1"}, false},
	}
	for _, c := range casos {
		err := Novo(c.cfg).Verificar(context.Background())
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.nome, err)
		}
		if !c.ok && !errors.Is(err, transcricao.ErrMotorAusente) {
			t.Errorf("%s: erro = %v, queria ErrMotorAusente", c.nome, err)
		}
	}
	if n := len(s.recebidos()); n != 0 {
		t.Errorf("Verificar foi à rede %d vezes", n)
	}
}
