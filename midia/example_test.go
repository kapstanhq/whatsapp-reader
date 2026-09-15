package midia_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/kapstanhq/whatsapp-reader/midia"
)

// O ciclo inteiro de um anexo: guardar no handler, baixar num worker, e pedir
// ao celular quando o link venceu. Compila, mas não roda: precisa de um
// cliente pareado.
func Example() {
	var cli *whatsmeow.Client // já conectado
	var evt *events.Message   // recebido no handler

	// No handler: só extrair e guardar. Nada de rede aqui.
	a, ok := midia.Extrair(evt.Message)
	if !ok {
		return
	}
	guardado, _ := a.Bytes()

	// Num worker, depois:
	a, _ = midia.Ler(midia.Audio, guardado)
	destino := filepath.Join("midia", fmt.Sprintf("%x%s", a.SHA256(), a.Extensao()))
	err := midia.Baixar(context.Background(), cli, a, destino)
	switch {
	case errors.Is(err, midia.ErrVencido):
		// A resposta chega depois, como *events.MediaRetry; abra com midia.RespostaDoCelular.
		_ = midia.PedirAoCelular(context.Background(), cli, a, evt.Info)
	case errors.Is(err, midia.ErrCorrompido):
		// Desistir: repetir não conserta.
	}
}
