package transcricao

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClassificar(t *testing.T) {
	casos := []struct {
		nome string
		err  error
		quer Classe
	}{
		{"sem erro", nil, Ok},
		{"rede", errors.New("read tcp: connection reset by peer"), Passageiro},
		{"prazo estourado", fmt.Errorf("whisper.cpp: passou de 2m0s: %w", context.DeadlineExceeded), Passageiro},
		{"limite com espera", fmt.Errorf("openai: %w", &ErroLimite{Depois: 30 * time.Second}), Passageiro},
		{"cancelado", fmt.Errorf("whisper.cpp: %w", context.Canceled), Cancelado},
		{"executável ausente", fmt.Errorf("ffmpeg: %w", ErrMotorAusente), Configuracao},
		{"chave recusada, embrulhada duas vezes", fmt.Errorf("transcrever: %w", fmt.Errorf("openai: 401: %w", ErrCredencial)), Configuracao},
		{"áudio ilegível", fmt.Errorf("ffmpeg: %w", ErrAudioInvalido), Definitivo},
		{"cancelamento vence o resto", fmt.Errorf("%w: %w", context.Canceled, ErrAudioInvalido), Cancelado},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := Classificar(c.err); got != c.quer {
				t.Errorf("Classificar(%v) = %s, queria %s", c.err, got, c.quer)
			}
		})
	}
}

func TestErroLimite(t *testing.T) {
	causa := errors.New("429 Too Many Requests")
	err := fmt.Errorf("openai: %w", &ErroLimite{Depois: 20 * time.Second, Err: causa})

	if !errors.Is(err, ErrLimite) {
		t.Error("ErroLimite devia ser ErrLimite")
	}
	if !errors.Is(err, causa) {
		t.Error("ErroLimite devia deixar ver a causa")
	}
	var limite *ErroLimite
	if !errors.As(err, &limite) || limite.Depois != 20*time.Second {
		t.Errorf("o tempo pedido se perdeu no embrulho: %+v", limite)
	}
	if msg := err.Error(); !strings.Contains(msg, "20s") || !strings.Contains(msg, "429") {
		t.Errorf("mensagem sem o tempo ou a causa: %q", msg)
	}
	if msg := (&ErroLimite{}).Error(); msg != ErrLimite.Error() {
		t.Errorf("sem tempo nem causa, a mensagem devia ser a do sentinela: %q", msg)
	}
}
