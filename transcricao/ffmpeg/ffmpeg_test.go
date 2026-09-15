package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

/* O ffmpeg destes testes é o próprio binário de teste: com FFMPEG_FALSO no
   ambiente, TestMain vira um ffmpeg de mentira que faz o que o cenário manda.
   Assim o teste passa pelo processo de verdade — argumentos, código de saída,
   stderr, morte no cancelamento — sem exigir ffmpeg instalado no CI. */

func TestMain(m *testing.M) {
	if cenario := os.Getenv("FFMPEG_FALSO"); cenario != "" {
		os.Exit(ffmpegFalso(cenario))
	}
	os.Exit(m.Run())
}

func ffmpegFalso(cenario string) int {
	args := os.Args[1:]
	os.WriteFile(os.Getenv("FFMPEG_FALSO_ARGV"), []byte(strings.Join(args, "\n")), 0o600)
	destino := args[len(args)-1]
	switch cenario {
	case "ok":
		os.WriteFile(destino, make([]byte, tamanhoCabecalhoWAV+32000), 0o600)
		return 0
	case "ilegivel":
		os.WriteFile(destino, []byte("RIFF"), 0o600) // o que tiver começado a escrever
		fmt.Fprintln(os.Stderr, "[in#0 @ 0000021c4a] Error opening input: Invalid data found when processing input")
		fmt.Fprintln(os.Stderr, "Error opening input file nota.ogg.")
		return 1
	case "sem-amostras":
		os.WriteFile(destino, make([]byte, tamanhoCabecalhoWAV), 0o600)
		return 0
	case "sem-saida":
		return 0
	case "travado":
		time.Sleep(time.Minute)
		return 0
	}
	return 99
}

type cena struct {
	origem, destino, argv string
	c                     *Conversor
}

func novaCena(t *testing.T, cenario string) *cena {
	t.Helper()
	dir := t.TempDir()
	c := &cena{
		origem:  filepath.Join(dir, "nota.ogg"),
		destino: filepath.Join(dir, "entrada.wav"),
		argv:    filepath.Join(dir, "argv.txt"),
	}
	if err := os.WriteFile(c.origem, []byte("OggS"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.c = Novo(Config{Executavel: os.Args[0], Preparar: func(cmd *exec.Cmd) {
		cmd.Env = append(os.Environ(), "FFMPEG_FALSO="+cenario, "FFMPEG_FALSO_ARGV="+c.argv)
	}})
	return c
}

func (c *cena) chamouOFFmpeg() bool {
	_, err := os.Stat(c.argv)
	return err == nil
}

func depois(args []string, opcao string) string {
	for i := range len(args) - 1 {
		if args[i] == opcao {
			return args[i+1]
		}
	}
	return ""
}

func TestConverteParaWAV16kHzMono(t *testing.T) {
	c := novaCena(t, "ok")
	if err := c.c.ParaWAV(context.Background(), c.origem, c.destino); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(c.destino); err != nil || st.Size() != tamanhoCabecalhoWAV+32000 {
		t.Errorf("WAV no disco: %v, %v", st, err)
	}
	dados, _ := os.ReadFile(c.argv)
	args := strings.Split(string(dados), "\n")
	for opcao, valor := range map[string]string{"-i": c.origem, "-ar": "16000", "-ac": "1", "-c:a": "pcm_s16le", "-f": "wav"} {
		if got := depois(args, opcao); got != valor {
			t.Errorf("%s = %q, queria %q", opcao, got, valor)
		}
	}
	for _, opcao := range []string{"-nostdin", "-y", "-vn"} {
		if !slices.Contains(args, opcao) {
			t.Errorf("faltou %s em %q", opcao, args)
		}
	}
	if args[len(args)-1] != c.destino {
		t.Errorf("o destino devia ser o último argumento: %q", args)
	}
}

func TestFalhasDoFFmpeg(t *testing.T) {
	casos := []struct {
		nome, cenario string
		ajuste        func(*cena)
		sentinela     error
		classe        transcricao.Classe
		chama         bool
	}{
		{"áudio que o ffmpeg não lê", "ilegivel", nil, transcricao.ErrAudioInvalido, transcricao.Definitivo, true},
		{"WAV sem nenhuma amostra", "sem-amostras", nil, transcricao.ErrAudioInvalido, transcricao.Definitivo, true},
		{"saiu com 0 sem escrever", "sem-saida", nil, transcricao.ErrAudioInvalido, transcricao.Definitivo, true},
		{"ffmpeg não instalado", "ok", func(c *cena) {
			c.c.cfg.Executavel = filepath.Join(filepath.Dir(c.origem), "nao-existe", "ffmpeg")
		}, transcricao.ErrMotorAusente, transcricao.Configuracao, false},
		// Origem sumida não condena o áudio: dá para baixar de novo.
		{"origem sumiu", "ok", func(c *cena) { os.Remove(c.origem) }, fs.ErrNotExist, transcricao.Passageiro, false},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			c := novaCena(t, caso.cenario)
			if caso.ajuste != nil {
				caso.ajuste(c)
			}
			err := c.c.ParaWAV(context.Background(), c.origem, c.destino)
			if !errors.Is(err, caso.sentinela) {
				t.Fatalf("erro = %v, queria %v", err, caso.sentinela)
			}
			if got := transcricao.Classificar(err); got != caso.classe {
				t.Errorf("classe = %s, queria %s (%v)", got, caso.classe, err)
			}
			if _, err := os.Stat(c.destino); !errors.Is(err, fs.ErrNotExist) {
				t.Error("o WAV pela metade ficou no disco")
			}
			if c.chamouOFFmpeg() != caso.chama {
				t.Errorf("chamou o ffmpeg = %v, queria %v", c.chamouOFFmpeg(), caso.chama)
			}
		})
	}
}

func TestErroTrazAFraseDoFFmpeg(t *testing.T) {
	c := novaCena(t, "ilegivel")
	err := c.c.ParaWAV(context.Background(), c.origem, c.destino)
	if err == nil || !strings.Contains(err.Error(), "Error opening input file nota.ogg.") {
		t.Errorf("a mensagem devia trazer a linha de erro do ffmpeg: %v", err)
	}
}

func TestCanceladoEncerraOFFmpeg(t *testing.T) {
	c := novaCena(t, "travado")
	ctx, cancelar := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancelar)
	inicio := time.Now()
	err := c.c.ParaWAV(ctx, c.origem, c.destino)
	if !errors.Is(err, context.Canceled) || transcricao.Classificar(err) != transcricao.Cancelado {
		t.Fatalf("erro = %v, queria cancelado", err)
	}
	if d := time.Since(inicio); d > 10*time.Second {
		t.Errorf("levou %s para largar o processo", d)
	}
}

func TestVerificar(t *testing.T) {
	if err := Novo(Config{Executavel: os.Args[0]}).Verificar(context.Background()); err != nil {
		t.Errorf("executável presente e Verificar reclamou: %v", err)
	}
	err := Novo(Config{Executavel: filepath.Join(t.TempDir(), "ffmpeg")}).Verificar(context.Background())
	if !errors.Is(err, transcricao.ErrMotorAusente) {
		t.Errorf("executável ausente: erro = %v", err)
	}
}
