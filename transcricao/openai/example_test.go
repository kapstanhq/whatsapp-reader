package openai_test

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
	"github.com/kapstanhq/whatsapp-reader/transcricao/openai"
)

// Compila, mas não roda: precisa de uma chave. O áudio sai desta máquina.
func Example() {
	motor := openai.Novo(openai.Config{
		URL:    "https://api.groq.com/openai/v1",
		Chave:  os.Getenv("GROQ_API_KEY"), // quem lê o ambiente é quem chama
		Modelo: "whisper-large-v3-turbo",
	})

	r, err := motor.Transcrever(context.Background(),
		transcricao.Audio{Caminho: "nota.ogg"},
		transcricao.Pedido{Idioma: "pt"})
	var limite *transcricao.ErroLimite
	switch {
	case errors.As(err, &limite):
		fmt.Println("o serviço pediu para esperar", limite.Depois)
	case err != nil:
		fmt.Println(transcricao.Classificar(err), err)
	default:
		fmt.Println(r.Texto)
	}
}
