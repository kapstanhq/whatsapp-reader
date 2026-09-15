package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

// TamanhoMaxPadrao é o limite de upload da API da OpenAI.
const TamanhoMaxPadrao = 25 << 20

// Config do motor. URL e Modelo são obrigatórios.
type Config struct {
	// URL base da API, até o /v1: "https://api.openai.com/v1",
	// "https://api.groq.com/openai/v1", "http://localhost:8000/v1". Aceita
	// também o endereço completo, terminado em /audio/transcriptions.
	URL string
	// Chave vai no cabeçalho Authorization. Vazia não manda o cabeçalho
	// (servidor local sem autenticação).
	Chave string
	// Modelo: "whisper-1", "gpt-4o-mini-transcribe", "whisper-large-v3-turbo".
	Modelo string
	// CampoIdioma é o nome do campo do idioma no formulário. Vazio é "language".
	CampoIdioma string
	// TamanhoMax em bytes; arquivo maior nem é enviado. Zero é [TamanhoMaxPadrao].
	TamanhoMax int64
	// HTTP é o cliente usado. Nil é um com prazo de cinco minutos por requisição.
	HTTP *http.Client
}

// Motor transcreve mandando o arquivo inteiro numa requisição.
type Motor struct {
	cfg Config
}

var (
	_ transcricao.Motor       = (*Motor)(nil)
	_ transcricao.Verificador = (*Motor)(nil)
)

func Novo(cfg Config) *Motor {
	if cfg.CampoIdioma == "" {
		cfg.CampoIdioma = "language"
	}
	if cfg.TamanhoMax <= 0 {
		cfg.TamanhoMax = TamanhoMaxPadrao
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Motor{cfg: cfg}
}

// Verificar confere a configuração sem ir à rede: uma requisição de teste a
// cada lote não diria mais do que a primeira transcrição diz.
func (m *Motor) Verificar(context.Context) error {
	if _, err := m.endereco(); err != nil {
		return err
	}
	if m.cfg.Modelo == "" {
		return fmt.Errorf("openai: nenhum modelo configurado: %w", transcricao.ErrMotorAusente)
	}
	return nil
}

func (m *Motor) Transcrever(ctx context.Context, a transcricao.Audio, p transcricao.Pedido) (transcricao.Resultado, error) {
	inicio := time.Now()
	var nada transcricao.Resultado
	if err := m.Verificar(ctx); err != nil {
		return nada, err
	}
	endereco, _ := m.endereco()
	corpo, tipo, err := m.formulario(a, p)
	if err != nil {
		return nada, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endereco, corpo)
	if err != nil {
		return nada, fmt.Errorf("openai: %w", err)
	}
	req.Header.Set("Content-Type", tipo)
	req.Header.Set("Accept", "application/json")
	if m.cfg.Chave != "" {
		req.Header.Set("Authorization", "Bearer "+m.cfg.Chave)
	}

	resp, err := m.cfg.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nada, fmt.Errorf("openai: %w", ctx.Err())
		}
		return nada, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	dados, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		if ctx.Err() != nil {
			return nada, fmt.Errorf("openai: %w", ctx.Err())
		}
		return nada, fmt.Errorf("openai: lendo a resposta: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nada, m.falha(resp, dados)
	}
	var r struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(dados, &r); err != nil || r.Text == nil {
		return nada, fmt.Errorf("openai: resposta sem o campo text: %s", m.trecho(dados))
	}
	return transcricao.Resultado{
		Texto:  strings.TrimSpace(*r.Text),
		Idioma: p.Idioma,
		Motor:  "openai",
		Modelo: m.cfg.Modelo,
		Levou:  time.Since(inicio),
	}, nil
}

func (m *Motor) endereco() (string, error) {
	u, err := url.Parse(m.cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		// A URL fica fora da mensagem: pode ter usuário e senha embutidos.
		return "", fmt.Errorf("openai: a URL da API precisa ser http:// ou https:// com um endereço: %w", transcricao.ErrMotorAusente)
	}
	base := strings.TrimRight(m.cfg.URL, "/")
	if strings.HasSuffix(base, "/audio/transcriptions") {
		return base, nil
	}
	return base + "/audio/transcriptions", nil
}

// O arquivo inteiro vai para a memória: o limite da API é pequeno, e com
// Content-Length conhecido servidor nenhum recusa o envio por ser em pedaços.
func (m *Motor) formulario(a transcricao.Audio, p transcricao.Pedido) (io.Reader, string, error) {
	f, err := os.Open(a.Caminho)
	if err != nil {
		return nil, "", fmt.Errorf("openai: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("openai: %w", err)
	}
	if st.Size() > m.cfg.TamanhoMax {
		return nil, "", fmt.Errorf("openai: o áudio tem %.1f MB, acima do limite de %.1f MB da API: %w",
			float64(st.Size())/(1<<20), float64(m.cfg.TamanhoMax)/(1<<20), transcricao.ErrAudioInvalido)
	}

	var corpo bytes.Buffer
	w := multipart.NewWriter(&corpo)
	// O nome no disco não sai; a extensão vai, porque é por ela que a API
	// reconhece o formato.
	parte, err := w.CreateFormFile("file", "audio"+extensao(a))
	if err == nil {
		_, err = io.Copy(parte, f)
	}
	campos := [][2]string{{"model", m.cfg.Modelo}, {"response_format", "json"}}
	if p.Idioma != "" {
		campos = append(campos, [2]string{m.cfg.CampoIdioma, p.Idioma})
	}
	if p.Dica != "" {
		campos = append(campos, [2]string{"prompt", p.Dica})
	}
	for _, c := range campos {
		if err == nil {
			err = w.WriteField(c[0], c[1])
		}
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		return nil, "", fmt.Errorf("openai: montando o formulário: %w", err)
	}
	return &corpo, w.FormDataContentType(), nil
}

func extensao(a transcricao.Audio) string {
	if ext := strings.ToLower(filepath.Ext(a.Caminho)); ext != "" {
		return ext
	}
	mime := strings.ToLower(a.Mime)
	for prefixo, ext := range map[string]string{
		"audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/x-m4a": ".m4a", "audio/aac": ".m4a",
		"audio/wav": ".wav", "audio/x-wav": ".wav", "audio/webm": ".webm", "audio/flac": ".flac",
	} {
		if strings.HasPrefix(mime, prefixo) {
			return ext
		}
	}
	return ".ogg" // nota de voz do WhatsApp
}

type erroDaAPI struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   string `json:"param"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (m *Motor) falha(resp *http.Response, dados []byte) error {
	var e erroDaAPI
	// Corpo fora do formato (proxy, servidor que não é a OpenAI): vale o texto cru.
	_ = json.Unmarshal(dados, &e)
	msg := m.semChave(e.Error.Message)
	if msg == "" {
		msg = m.trecho(dados)
	}
	resumo := strings.TrimSpace(fmt.Sprintf("%d %s", resp.StatusCode, msg))

	switch s := resp.StatusCode; {
	case e.Error.Code == "insufficient_quota" || e.Error.Type == "insufficient_quota":
		return fmt.Errorf("openai: a conta está sem crédito (%d): %w", s, transcricao.ErrCredencial)
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		// A mensagem do serviço fica de fora: a da OpenAI repete parte da chave.
		return fmt.Errorf("openai: a chave foi recusada (%d): %w", s, transcricao.ErrCredencial)
	case s == http.StatusTooManyRequests:
		return fmt.Errorf("openai: %w", &transcricao.ErroLimite{
			Depois: esperaPedida(resp.Header, time.Now()),
			Err:    fmt.Errorf("%s", resumo),
		})
	case s == http.StatusNotFound, e.Error.Param == "model", strings.Contains(e.Error.Code, "model"):
		return fmt.Errorf("openai: %s: %w", resumo, transcricao.ErrMotorAusente)
	case s == http.StatusBadRequest, s == http.StatusRequestEntityTooLarge,
		s == http.StatusUnsupportedMediaType, s == http.StatusUnprocessableEntity:
		return fmt.Errorf("openai: %s: %w", resumo, transcricao.ErrAudioInvalido)
	}
	return fmt.Errorf("openai: %s", resumo)
}

// esperaPedida lê o Retry-After, em segundos ou como data HTTP. Zero se não houver.
func esperaPedida(h http.Header, agora time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(agora) {
		return t.Sub(agora)
	}
	return 0
}

func (m *Motor) semChave(s string) string {
	if m.cfg.Chave == "" {
		return s
	}
	return strings.ReplaceAll(s, m.cfg.Chave, "<chave>")
}

// trecho é o começo do corpo, numa linha, para caber numa mensagem de erro.
func (m *Motor) trecho(dados []byte) string {
	s := strings.Join(strings.Fields(string(dados)), " ")
	if len(s) > 300 {
		s = strings.ToValidUTF8(s[:300], "") + "…"
	}
	return m.semChave(s)
}
