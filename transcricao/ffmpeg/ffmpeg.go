package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

// Config do conversor. O zero vale: ffmpeg do PATH, prioridade normal.
type Config struct {
	// Executavel: caminho ou nome no PATH. Vazio é "ffmpeg".
	Executavel string
	// Preparar, se não for nil, recebe o comando antes de ele rodar — para
	// baixar a prioridade, trocar o ambiente.
	Preparar func(*exec.Cmd)
}

// Conversor chama o ffmpeg, um processo por arquivo.
type Conversor struct {
	cfg Config
}

var (
	_ transcricao.Conversor   = (*Conversor)(nil)
	_ transcricao.Verificador = (*Conversor)(nil)
)

// O cabeçalho de um WAV PCM: um arquivo deste tamanho não tem nenhuma amostra.
const tamanhoCabecalhoWAV = 44

func Novo(cfg Config) *Conversor {
	if cfg.Executavel == "" {
		cfg.Executavel = "ffmpeg"
	}
	return &Conversor{cfg: cfg}
}

// Verificar confere que o executável existe. Não roda o ffmpeg.
func (c *Conversor) Verificar(context.Context) error {
	_, err := c.executavel()
	return err
}

// ParaWAV grava em destino o áudio de origem como WAV PCM 16 kHz mono. Em
// erro, destino não fica no disco.
func (c *Conversor) ParaWAV(ctx context.Context, origem, destino string) error {
	if _, err := os.Stat(origem); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	exe, err := c.executavel()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", origem,
		"-vn", "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "-f", "wav",
		destino)
	var saida rabo
	cmd.Stderr = &saida
	cmd.WaitDelay = 2 * time.Second
	if c.cfg.Preparar != nil {
		c.cfg.Preparar(cmd)
	}
	if err := cmd.Run(); err != nil {
		os.Remove(destino)
		if ctx.Err() != nil {
			return fmt.Errorf("ffmpeg: %w", ctx.Err())
		}
		return fmt.Errorf("ffmpeg: não converteu %s (%s): %w", filepath.Base(origem), saida.resumo(), transcricao.ErrAudioInvalido)
	}
	st, err := os.Stat(destino)
	switch {
	case err != nil:
		return fmt.Errorf("ffmpeg: terminou sem escrever o WAV de %s: %w", filepath.Base(origem), transcricao.ErrAudioInvalido)
	case st.Size() <= tamanhoCabecalhoWAV:
		os.Remove(destino)
		return fmt.Errorf("ffmpeg: o WAV de %s saiu sem nenhuma amostra: %w", filepath.Base(origem), transcricao.ErrAudioInvalido)
	}
	return nil
}

func (c *Conversor) executavel() (string, error) {
	caminho, err := exec.LookPath(c.cfg.Executavel)
	if err != nil {
		return "", fmt.Errorf("ffmpeg: %v: %w", err, transcricao.ErrMotorAusente)
	}
	return caminho, nil
}
