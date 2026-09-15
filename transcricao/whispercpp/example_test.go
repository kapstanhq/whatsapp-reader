package whispercpp_test

import (
	"context"
	"fmt"
	"time"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
	"github.com/kapstanhq/whatsapp-reader/transcricao/ffmpeg"
	"github.com/kapstanhq/whatsapp-reader/transcricao/whispercpp"
)

// Compila, mas não roda: precisa da whisper-cli, do ffmpeg e de um modelo.
func Example() {
	motor := whispercpp.Novo(whispercpp.Config{
		Modelo:    "modelos/ggml-large-v3-turbo-q5_0.bin",
		Conversor: ffmpeg.Novo(ffmpeg.Config{}),
	})
	ctx := context.Background()
	if err := motor.Verificar(ctx); err != nil {
		fmt.Println("falta instalar:", err)
		return
	}

	r, err := motor.Transcrever(ctx,
		transcricao.Audio{Caminho: "nota.ogg", Duracao: 42 * time.Second},
		transcricao.Pedido{Idioma: "pt", Dica: "calhas, rufo, seu João"})
	switch transcricao.Classificar(err) {
	case transcricao.Ok:
		fmt.Println(r.Texto)
	case transcricao.Passageiro:
		// tentar este áudio de novo mais tarde
	case transcricao.Definitivo:
		// este áudio não vai dar; seguir com os outros
	case transcricao.Configuracao:
		// parar e avisar: nenhum outro vai dar
	case transcricao.Cancelado:
		// não contar a tentativa
	}
}
