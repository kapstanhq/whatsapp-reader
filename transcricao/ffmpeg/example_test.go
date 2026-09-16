package ffmpeg_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
	"github.com/kapstanhq/whatsapp-reader/transcricao/ffmpeg"
)

// Compila, mas não roda: precisa do ffmpeg instalado.
func Example() {
	conversor := ffmpeg.Novo(ffmpeg.Config{}) // o ffmpeg do PATH

	err := conversor.ParaWAV(context.Background(), "nota.ogg", "nota.wav")
	switch {
	case errors.Is(err, transcricao.ErrMotorAusente):
		fmt.Println("instale o ffmpeg: scoop install ffmpeg | brew install ffmpeg")
	case errors.Is(err, transcricao.ErrAudioInvalido):
		fmt.Println("este arquivo não tem áudio legível")
	case err != nil:
		fmt.Println("tente de novo mais tarde:", err)
	}
}
