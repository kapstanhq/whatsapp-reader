package whispercpp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

// Config do motor. Modelo é obrigatório; Conversor, para qualquer áudio que
// não seja WAV.
type Config struct {
	// Executavel: caminho ou nome no PATH. Vazio é "whisper-cli".
	Executavel string
	// Modelo: arquivo ggml (ggml-large-v3-turbo-q5_0.bin, ggml-small-q5_1.bin).
	Modelo string
	// Threads da whisper-cli. Zero é metade dos núcleos: sobra máquina para
	// quem está usando.
	Threads int
	// Conversor transforma o áudio no WAV de 16 kHz que a whisper-cli lê. Nil
	// passa o arquivo como veio.
	Conversor transcricao.Conversor
	// DirTemp: onde nasce a pasta de trabalho de cada transcrição. Vazio é o
	// temporário do sistema.
	DirTemp string
	// Limite recebe a duração declarada do áudio e devolve quanto esperar antes
	// de encerrar o processo. Nil é [LimitePadrao].
	Limite func(duracao time.Duration) time.Duration
	// Preparar, se não for nil, recebe o comando antes de ele rodar — para
	// baixar a prioridade, trocar o ambiente.
	Preparar func(*exec.Cmd)
}

// Motor transcreve chamando a whisper-cli, um processo por áudio.
type Motor struct {
	cfg Config
}

var (
	_ transcricao.Motor       = (*Motor)(nil)
	_ transcricao.Verificador = (*Motor)(nil)
)

func Novo(cfg Config) *Motor {
	if cfg.Executavel == "" {
		cfg.Executavel = "whisper-cli"
	}
	if cfg.Threads <= 0 {
		cfg.Threads = max(1, runtime.NumCPU()/2)
	}
	if cfg.Limite == nil {
		cfg.Limite = LimitePadrao
	}
	return &Motor{cfg: cfg}
}

// LimitePadrao dá dois minutos para carregar o modelo e três vezes a duração
// do áudio para transcrever. Sem duração, supõe cinco minutos de áudio.
func LimitePadrao(duracao time.Duration) time.Duration {
	if duracao <= 0 {
		duracao = 5 * time.Minute
	}
	return 2*time.Minute + 3*duracao
}

// Verificar confere o executável, o modelo e, se ele souber se verificar, o
// conversor. Não roda nada.
func (m *Motor) Verificar(ctx context.Context) error {
	if _, err := m.executavel(); err != nil {
		return err
	}
	if _, err := m.modelo(); err != nil {
		return err
	}
	if v, ok := m.cfg.Conversor.(transcricao.Verificador); ok {
		return v.Verificar(ctx)
	}
	return nil
}

func (m *Motor) Transcrever(ctx context.Context, a transcricao.Audio, p transcricao.Pedido) (transcricao.Resultado, error) {
	inicio := time.Now()
	var nada transcricao.Resultado
	exe, err := m.executavel()
	if err != nil {
		return nada, err
	}
	modelo, err := m.modelo()
	if err != nil {
		return nada, err
	}
	origem, err := filepath.Abs(a.Caminho)
	if err != nil {
		return nada, fmt.Errorf("whisper.cpp: %w", err)
	}
	trabalho, err := m.pastaDeTrabalho()
	if err != nil {
		return nada, err
	}
	defer os.RemoveAll(trabalho)

	prazo := m.cfg.Limite(a.Duracao)
	job, cancelar := context.WithTimeout(ctx, prazo)
	defer cancelar()

	entrada := origem
	if m.cfg.Conversor != nil {
		entrada = filepath.Join(trabalho, "entrada.wav")
		if err := m.cfg.Conversor.ParaWAV(job, origem, entrada); err != nil {
			if e := parou(ctx, job, prazo); e != nil {
				return nada, e
			}
			return nada, err
		}
	}

	idioma := p.Idioma
	if idioma == "" {
		idioma = "auto" // o padrão da whisper-cli é inglês, não detectar
	}
	args := []string{
		"-m", relativo(trabalho, modelo),
		"-f", relativo(trabalho, entrada),
		"-l", idioma,
		"-t", strconv.Itoa(m.cfg.Threads),
		"-nt", "-oj", "-of", "saida",
	}
	if p.Dica != "" {
		// No Windows a dica passa pela mesma página de código dos caminhos (ver
		// relativo): acento chega como byte da página, não como UTF-8.
		args = append(args, "--prompt", p.Dica)
	}
	cmd := exec.CommandContext(job, exe, args...)
	cmd.Dir = trabalho
	var saida rabo
	cmd.Stderr = &saida
	cmd.WaitDelay = 2 * time.Second
	if m.cfg.Preparar != nil {
		m.cfg.Preparar(cmd)
	}
	if err := cmd.Run(); err != nil {
		if e := parou(ctx, job, prazo); e != nil {
			return nada, e
		}
		if sentinela := pelaSaida(saida.String()); sentinela != nil {
			return nada, fmt.Errorf("whisper.cpp: %s: %w", saida.resumo(), sentinela)
		}
		return nada, fmt.Errorf("whisper.cpp: %s: %w", saida.resumo(), err)
	}

	/* Sucesso é o arquivo, não o código de saída: a whisper-cli pula a entrada
	   que não encontra ou não consegue ler e termina com 0 (examples/cli/cli.cpp,
	   "failed to read audio file" seguido de continue). */
	dados, err := os.ReadFile(filepath.Join(trabalho, "saida.json"))
	if err != nil {
		sentinela := pelaSaida(saida.String())
		if sentinela == nil {
			sentinela = transcricao.ErrAudioInvalido
		}
		return nada, fmt.Errorf("whisper.cpp: terminou sem escrever a transcrição (%s): %w", saida.resumo(), sentinela)
	}
	texto, detectado, err := lerSaida(dados)
	if err != nil {
		return nada, fmt.Errorf("whisper.cpp: a transcrição saiu ilegível: %w", err)
	}
	if detectado == "" || detectado == "auto" {
		detectado = p.Idioma
	}
	return transcricao.Resultado{
		Texto:  texto,
		Idioma: detectado,
		Motor:  "whisper.cpp",
		Modelo: nomeDoModelo(modelo),
		Levou:  time.Since(inicio),
	}, nil
}

func (m *Motor) executavel() (string, error) {
	caminho, err := exec.LookPath(m.cfg.Executavel)
	if err == nil {
		// Absoluto: o processo roda com a pasta de trabalho como diretório atual.
		caminho, err = filepath.Abs(caminho)
	}
	if err != nil {
		return "", fmt.Errorf("whisper.cpp: %v: %w", err, transcricao.ErrMotorAusente)
	}
	return caminho, nil
}

func (m *Motor) modelo() (string, error) {
	if m.cfg.Modelo == "" {
		return "", fmt.Errorf("whisper.cpp: nenhum modelo configurado: %w", transcricao.ErrMotorAusente)
	}
	caminho, err := filepath.Abs(m.cfg.Modelo)
	if err == nil {
		var st os.FileInfo
		if st, err = os.Stat(caminho); err == nil && st.IsDir() {
			err = fmt.Errorf("%s é uma pasta, não um arquivo ggml", caminho)
		}
	}
	if err != nil {
		return "", fmt.Errorf("whisper.cpp: modelo: %v: %w", err, transcricao.ErrMotorAusente)
	}
	return caminho, nil
}

func (m *Motor) pastaDeTrabalho() (string, error) {
	if m.cfg.DirTemp != "" {
		if err := os.MkdirAll(m.cfg.DirTemp, 0o755); err != nil {
			return "", fmt.Errorf("whisper.cpp: pasta temporária: %w", err)
		}
	}
	dir, err := os.MkdirTemp(m.cfg.DirTemp, "whisper-")
	if err == nil {
		var abs string
		if abs, err = filepath.Abs(dir); err != nil {
			os.RemoveAll(dir)
		}
		dir = abs
	}
	if err != nil {
		return "", fmt.Errorf("whisper.cpp: pasta de trabalho: %w", err)
	}
	return dir, nil
}

// Quando o processo morre porque o contexto acabou, o erro dele ("exit status
// 1") não explica nada; o que importa é quem desistiu.
func parou(ctx, job context.Context, prazo time.Duration) error {
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("whisper.cpp: %w", ctx.Err())
	case job.Err() != nil:
		return fmt.Errorf("whisper.cpp: passou de %s sem terminar: %w", prazo, job.Err())
	}
	return nil
}

// pelaSaida lê no stderr as frases de examples/cli/cli.cpp que dizem de quem é
// a culpa. Nil quando nenhuma aparece.
func pelaSaida(stderr string) error {
	switch {
	case strings.Contains(stderr, "unknown argument"):
		// whisper-cli antiga, sem uma opção que passamos.
		return transcricao.ErrMotorAusente
	case strings.Contains(stderr, "failed to initialize whisper context"),
		strings.Contains(stderr, "failed to load model"):
		return transcricao.ErrMotorAusente
	case strings.Contains(stderr, "failed to read audio"),
		strings.Contains(stderr, "input file not found"):
		return transcricao.ErrAudioInvalido
	}
	return nil
}

// relativo escreve alvo a partir da pasta de trabalho. A whisper-cli recebe os
// argumentos em char*, e no Windows eles chegam na página de código do sistema,
// não em UTF-8 (cli.cpp não usa wmain nem CommandLineToArgvW): um caminho com
// caractere fora dela não abre. A pasta de trabalho vai como diretório atual,
// que não passa pela linha de comando; relativo a ela, o trecho comum — a
// pasta do usuário, onde mora o acento — some.
func relativo(base, alvo string) string {
	if r, err := filepath.Rel(base, alvo); err == nil {
		return r
	}
	return alvo
}

type saidaJSON struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Text string `json:"text"`
	} `json:"transcription"`
}

// lerSaida junta os segmentos. A whisper-cli só escapa aspas e barra invertida
// (escape_double_quotes_and_backslashes em cli.cpp): quebra de linha ou
// tabulação dentro do texto sai crua e invalida o JSON. Fora das strings,
// caractere de controle só aparece como espaço em branco, então trocar todos
// por espaço não muda a estrutura. Nenhum byte de sequência UTF-8 é menor que
// 0x20.
func lerSaida(dados []byte) (texto, idioma string, err error) {
	limpo := make([]byte, len(dados))
	for i, b := range dados {
		if b < 0x20 {
			b = ' '
		}
		limpo[i] = b
	}
	var s saidaJSON
	if err := json.Unmarshal(limpo, &s); err != nil {
		return "", "", err
	}
	partes := make([]string, 0, len(s.Transcription))
	for _, seg := range s.Transcription {
		t := strings.Join(strings.Fields(seg.Text), " ")
		// Silêncio vira um segmento "[BLANK_AUDIO]": não é fala.
		if t == "" || t == "[BLANK_AUDIO]" {
			continue
		}
		partes = append(partes, t)
	}
	return strings.Join(partes, " "), s.Result.Language, nil
}

// "…/ggml-large-v3-turbo-q5_0.bin" → "large-v3-turbo-q5_0".
func nomeDoModelo(caminho string) string {
	nome := strings.TrimSuffix(filepath.Base(caminho), filepath.Ext(caminho))
	return strings.TrimPrefix(nome, "ggml-")
}
