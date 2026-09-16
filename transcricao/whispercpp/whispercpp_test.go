package whispercpp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

/* A whisper-cli destes testes é o próprio binário de teste: com WHISPER_FALSO
   no ambiente, TestMain vira uma whisper-cli de mentira. Ela confere o que a de
   verdade precisaria — modelo e entrada abrem a partir da pasta de trabalho —
   e responde como o cenário manda, com as frases de examples/cli/cli.cpp. As
   saídas em testdata foram escritas à mão no formato de output_json. */

func TestMain(m *testing.M) {
	if cenario := os.Getenv("WHISPER_FALSO"); cenario != "" {
		os.Exit(whisperFalso(cenario))
	}
	os.Exit(m.Run())
}

func whisperFalso(cenario string) int {
	args := os.Args[1:]
	os.WriteFile(os.Getenv("WHISPER_FALSO_ARGV"), []byte(strings.Join(args, "\n")), 0o600)
	saida := depois(args, "-of") + ".json"
	switch cenario {
	case "travado":
		time.Sleep(time.Minute)
		return 0
	case "sem-saida":
		fmt.Fprintf(os.Stderr, "error: failed to read audio file '%s'\n", depois(args, "-f"))
		return 0
	case "modelo-ruim":
		fmt.Fprintln(os.Stderr, "whisper_model_load: ERROR not all tensors loaded from model file")
		fmt.Fprintln(os.Stderr, "error: failed to initialize whisper context")
		return 3
	case "opcao-desconhecida":
		fmt.Fprintln(os.Stderr, "error: unknown argument: --prompt")
		fmt.Fprintln(os.Stderr, "\nusage: whisper-cli [options] file0 file1 ...")
		fmt.Fprintln(os.Stderr, "  -h,        --help              [default] show this help message and exit")
		return 0
	case "abortou":
		fmt.Fprintln(os.Stderr, "whisper_full_with_state: failed to encode")
		fmt.Fprintln(os.Stderr, "whisper-cli: failed to process audio")
		return 10
	case "json-truncado":
		os.WriteFile(saida, []byte(`{"transcription": [`), 0o600)
		return 0
	case "json-cru":
		os.WriteFile(saida, []byte("{\"result\": {\"language\": \"pt\"}, \"transcription\": [{\"text\": \" primeira linha\nsegunda\tlinha\"}]}"), 0o600)
		return 0
	}
	// "ok": responde com a fixture, se o modelo e a entrada abrirem daqui.
	if _, err := os.Stat(depois(args, "-m")); err != nil {
		fmt.Fprintf(os.Stderr, "whisper_init_from_file_with_params_no_state: failed to open '%s'\n", depois(args, "-m"))
		fmt.Fprintln(os.Stderr, "error: failed to initialize whisper context")
		return 3
	}
	if _, err := os.Stat(depois(args, "-f")); err != nil {
		fmt.Fprintf(os.Stderr, "error: input file not found '%s'\n", depois(args, "-f"))
		return 0
	}
	dados, err := os.ReadFile(os.Getenv("WHISPER_FALSO_JSON"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	os.WriteFile(saida, dados, 0o600)
	return 0
}

func depois(args []string, opcao string) string {
	for i := range len(args) - 1 {
		if args[i] == opcao {
			return args[i+1]
		}
	}
	return ""
}

type conversorFalso struct {
	err   error
	vezes int
}

func (c *conversorFalso) ParaWAV(_ context.Context, _, destino string) error {
	c.vezes++
	if c.err != nil {
		return c.err
	}
	return os.WriteFile(destino, make([]byte, 44+32000), 0o600)
}

type conversorQueSeVerifica struct {
	conversorFalso
	falta error
}

func (c *conversorQueSeVerifica) Verificar(context.Context) error { return c.falta }

type cena struct {
	m     *Motor
	audio transcricao.Audio
	temp  string
	argv  string
	conv  *conversorFalso
}

func novaCena(t *testing.T, cenario, fixture string) *cena {
	t.Helper()
	// Espaço e acento de propósito: é a pasta de muita gente no Windows.
	base := filepath.Join(t.TempDir(), "Área de trabalho", "João")
	c := &cena{
		temp: filepath.Join(base, "midia", ".tmp"),
		argv: filepath.Join(base, "argv.txt"),
		conv: &conversorFalso{},
	}
	modelo := filepath.Join(base, "modelos", "ggml-small-q5_1.bin")
	nota := filepath.Join(base, "midia", "ab", "ab01.ogg")
	for _, d := range []string{filepath.Dir(modelo), filepath.Dir(nota)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(modelo, []byte("ggml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nota, []byte("OggS"), 0o600); err != nil {
		t.Fatal(err)
	}
	json, err := filepath.Abs(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	c.m = Novo(Config{
		Executavel: os.Args[0],
		Modelo:     modelo,
		Threads:    3,
		Conversor:  c.conv,
		DirTemp:    c.temp,
		Preparar: func(cmd *exec.Cmd) {
			cmd.Env = append(os.Environ(), "WHISPER_FALSO="+cenario, "WHISPER_FALSO_ARGV="+c.argv, "WHISPER_FALSO_JSON="+json)
		},
	})
	c.audio = transcricao.Audio{Caminho: nota, Mime: "audio/ogg; codecs=opus", Duracao: 42 * time.Second}
	return c
}

func (c *cena) args(t *testing.T) []string {
	t.Helper()
	dados, err := os.ReadFile(c.argv)
	if err != nil {
		t.Fatalf("a whisper-cli não foi chamada: %v", err)
	}
	return strings.Split(string(dados), "\n")
}

func (c *cena) chamouAWhisperCli() bool {
	_, err := os.Stat(c.argv)
	return err == nil
}

func (c *cena) semSobras(t *testing.T) {
	t.Helper()
	if sobras, _ := os.ReadDir(c.temp); len(sobras) > 0 {
		t.Errorf("sobrou na pasta temporária: %v", sobras)
	}
}

func ascii(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func TestTranscreveNotaDeVoz(t *testing.T) {
	c := novaCena(t, "ok", "saida.json")
	r, err := c.m.Transcrever(context.Background(), c.audio, transcricao.Pedido{Idioma: "pt", Dica: "seu João, calhas"})
	if err != nil {
		t.Fatal(err)
	}
	if quer := "Oi, aqui é o João. Amanhã de manhã eu passo aí para ver as calhas."; r.Texto != quer {
		t.Errorf("texto = %q\n        queria %q", r.Texto, quer)
	}
	if r.Idioma != "pt" || r.Motor != "whisper.cpp" || r.Modelo != "small-q5_1" || r.Levou <= 0 {
		t.Errorf("resultado = %+v", r)
	}
	if c.conv.vezes != 1 {
		t.Errorf("o conversor rodou %d vezes", c.conv.vezes)
	}

	args := c.args(t)
	for opcao, valor := range map[string]string{"-l": "pt", "-t": "3", "-of": "saida", "--prompt": "seu João, calhas"} {
		if got := depois(args, opcao); got != valor {
			t.Errorf("%s = %q, queria %q", opcao, got, valor)
		}
	}
	for _, opcao := range []string{"-oj", "-nt"} {
		if !slices.Contains(args, opcao) {
			t.Errorf("faltou %s em %q", opcao, args)
		}
	}
	// A whisper-cli de mentira só achou os dois porque são relativos à pasta de
	// trabalho; aqui se confere que a parte acentuada ficou fora da linha de comando.
	for _, opcao := range []string{"-m", "-f"} {
		if v := depois(args, opcao); filepath.IsAbs(v) || !ascii(v) {
			t.Errorf("%s = %q: devia ser relativo e sem acento", opcao, v)
		}
	}
	c.semSobras(t)
}

func TestSemIdiomaDetectaESemDicaNaoMandaPrompt(t *testing.T) {
	c := novaCena(t, "ok", "saida.json")
	if _, err := c.m.Transcrever(context.Background(), c.audio, transcricao.Pedido{}); err != nil {
		t.Fatal(err)
	}
	args := c.args(t)
	if got := depois(args, "-l"); got != "auto" {
		t.Errorf("-l = %q: sem idioma a whisper-cli assume inglês", got)
	}
	if slices.Contains(args, "--prompt") {
		t.Errorf("sem dica não devia haver --prompt: %q", args)
	}
}

func TestSilencioESucessoComTextoVazio(t *testing.T) {
	c := novaCena(t, "ok", "silencio.json")
	r, err := c.m.Transcrever(context.Background(), c.audio, transcricao.Pedido{Idioma: "pt"})
	if err != nil || r.Texto != "" {
		t.Errorf("silêncio devia ser sucesso sem texto: %+v, %v", r, err)
	}
}

func TestQuebraDeLinhaCruaDentroDoTexto(t *testing.T) {
	c := novaCena(t, "json-cru", "")
	r, err := c.m.Transcrever(context.Background(), c.audio, transcricao.Pedido{Idioma: "pt"})
	if err != nil || r.Texto != "primeira linha segunda linha" {
		t.Errorf("texto = %q, %v", r.Texto, err)
	}
}

func TestSemConversorPassaOArquivoComoVeio(t *testing.T) {
	c := novaCena(t, "ok", "saida.json")
	c.m.cfg.Conversor = nil
	if _, err := c.m.Transcrever(context.Background(), c.audio, transcricao.Pedido{Idioma: "pt"}); err != nil {
		t.Fatal(err)
	}
	if got := depois(c.args(t), "-f"); filepath.Base(got) != "ab01.ogg" {
		t.Errorf("-f = %q, queria o próprio arquivo", got)
	}
}

func TestFalhasViramErroClassificado(t *testing.T) {
	casos := []struct {
		nome, cenario string
		ajuste        func(*cena)
		sentinela     error // nil: nenhum sentinela, só a classe
		classe        transcricao.Classe
		frase         string
		chama         bool
	}{
		{"pulou o arquivo e saiu com 0", "sem-saida", nil,
			transcricao.ErrAudioInvalido, transcricao.Definitivo, "failed to read audio file", true},
		{"modelo não carrega", "modelo-ruim", nil,
			transcricao.ErrMotorAusente, transcricao.Configuracao, "failed to initialize whisper context", true},
		{"whisper-cli sem uma opção", "opcao-desconhecida", nil,
			transcricao.ErrMotorAusente, transcricao.Configuracao, "unknown argument: --prompt", true},
		{"abortou no meio", "abortou", nil,
			nil, transcricao.Passageiro, "failed to process audio", true},
		{"JSON truncado", "json-truncado", nil,
			nil, transcricao.Passageiro, "ilegível", true},
		{"whisper-cli não instalada", "ok", func(c *cena) {
			c.m.cfg.Executavel = filepath.Join(c.temp, "nao-existe", "whisper-cli")
		}, transcricao.ErrMotorAusente, transcricao.Configuracao, "", false},
		{"modelo não baixado", "ok", func(c *cena) {
			c.m.cfg.Modelo = filepath.Join(filepath.Dir(c.m.cfg.Modelo), "ggml-nao-baixado.bin")
		}, transcricao.ErrMotorAusente, transcricao.Configuracao, "", false},
		{"ffmpeg recusou o áudio", "ok", func(c *cena) {
			c.conv.err = fmt.Errorf("ffmpeg: %w", transcricao.ErrAudioInvalido)
		}, transcricao.ErrAudioInvalido, transcricao.Definitivo, "ffmpeg", false},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			c := novaCena(t, caso.cenario, "saida.json")
			if caso.ajuste != nil {
				caso.ajuste(c)
			}
			_, err := c.m.Transcrever(context.Background(), c.audio, transcricao.Pedido{Idioma: "pt", Dica: "calhas"})
			if err == nil {
				t.Fatal("devia falhar")
			}
			if caso.sentinela != nil && !errors.Is(err, caso.sentinela) {
				t.Errorf("erro = %v, queria %v", err, caso.sentinela)
			}
			if got := transcricao.Classificar(err); got != caso.classe {
				t.Errorf("classe = %s, queria %s (%v)", got, caso.classe, err)
			}
			if !strings.Contains(err.Error(), caso.frase) {
				t.Errorf("a mensagem devia trazer %q: %v", caso.frase, err)
			}
			if c.chamouAWhisperCli() != caso.chama {
				t.Errorf("chamou a whisper-cli = %v, queria %v", c.chamouAWhisperCli(), caso.chama)
			}
			c.semSobras(t)
		})
	}
}

func TestPrazoEstouradoEncerraAWhisperCli(t *testing.T) {
	c := novaCena(t, "travado", "")
	c.m.cfg.Limite = func(time.Duration) time.Duration { return 500 * time.Millisecond }
	inicio := time.Now()
	_, err := c.m.Transcrever(context.Background(), c.audio, transcricao.Pedido{Idioma: "pt"})
	if !errors.Is(err, context.DeadlineExceeded) || transcricao.Classificar(err) != transcricao.Passageiro {
		t.Fatalf("erro = %v, queria prazo estourado e passageiro", err)
	}
	if d := time.Since(inicio); d > 10*time.Second {
		t.Errorf("levou %s para largar o processo", d)
	}
	c.semSobras(t)
}

func TestCanceladoEncerraAWhisperCli(t *testing.T) {
	c := novaCena(t, "travado", "")
	ctx, cancelar := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancelar)
	inicio := time.Now()
	_, err := c.m.Transcrever(ctx, c.audio, transcricao.Pedido{Idioma: "pt"})
	if !errors.Is(err, context.Canceled) || transcricao.Classificar(err) != transcricao.Cancelado {
		t.Fatalf("erro = %v, queria cancelado", err)
	}
	if d := time.Since(inicio); d > 10*time.Second {
		t.Errorf("levou %s para largar o processo", d)
	}
	c.semSobras(t)
}

func TestVerificar(t *testing.T) {
	ctx := context.Background()
	c := novaCena(t, "ok", "")
	if err := c.m.Verificar(ctx); err != nil {
		t.Errorf("tudo presente e Verificar reclamou: %v", err)
	}

	c.m.cfg.Conversor = &conversorQueSeVerifica{falta: fmt.Errorf("ffmpeg: %w", transcricao.ErrMotorAusente)}
	if err := c.m.Verificar(ctx); !errors.Is(err, transcricao.ErrMotorAusente) {
		t.Errorf("conversor ausente: erro = %v", err)
	}

	c.m.cfg.Modelo = ""
	if err := c.m.Verificar(ctx); !errors.Is(err, transcricao.ErrMotorAusente) {
		t.Errorf("sem modelo: erro = %v", err)
	}
	if c.chamouAWhisperCli() {
		t.Error("Verificar não pode rodar a whisper-cli")
	}
}

func TestNovoPreencheOPadrao(t *testing.T) {
	m := Novo(Config{Modelo: "ggml-small-q5_1.bin"})
	if m.cfg.Executavel != "whisper-cli" || m.cfg.Threads != max(1, runtime.NumCPU()/2) || m.cfg.Limite == nil {
		t.Errorf("config = %+v", m.cfg)
	}
	if got := LimitePadrao(42 * time.Second); got != 2*time.Minute+126*time.Second {
		t.Errorf("LimitePadrao(42s) = %s", got)
	}
	if got := LimitePadrao(0); got != 17*time.Minute {
		t.Errorf("LimitePadrao sem duração = %s", got)
	}
}
