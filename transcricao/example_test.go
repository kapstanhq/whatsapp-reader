package transcricao_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
)

// Uma fila decide o destino de cada áudio só pela classe do erro, sem saber
// qual motor rodou.
func ExampleClassificar() {
	erros := []error{
		nil,
		fmt.Errorf("whisper.cpp: terminou sem escrever a transcrição: %w", transcricao.ErrAudioInvalido),
		&transcricao.ErroLimite{Depois: 20 * time.Second, Err: errors.New("429")},
		fmt.Errorf("ffmpeg: %w", transcricao.ErrMotorAusente),
		fmt.Errorf("whisper.cpp: %w", context.Canceled),
	}
	for _, err := range erros {
		fmt.Println(transcricao.Classificar(err))
	}
	// Output:
	// ok
	// definitivo
	// passageiro
	// configuração
	// cancelado
}

func ExampleErroLimite() {
	err := fmt.Errorf("openai: %w", &transcricao.ErroLimite{Depois: 20 * time.Second})

	var limite *transcricao.ErroLimite
	if errors.As(err, &limite) {
		fmt.Println("esperar", limite.Depois)
	}
	// Output: esperar 20s
}
