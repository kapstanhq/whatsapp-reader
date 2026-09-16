/*
Package midia é a cola entre o whatsmeow e um arquivo no disco: tira o anexo de
uma mensagem, guarda o que é preciso para baixá-lo depois, baixa sem deixar
arquivo pela metade, e pede ao celular um link novo quando o antigo venceu.

O WhatsApp entrega o histórico UMA vez, no pareamento, e a chave de cada anexo
vem junto. Quem descarta a chave ali não baixa nunca mais. Por isso a unidade
deste pacote é o [Anexo]: o proto do anexo sem miniatura nem mensagem citada,
serializável com [Anexo.Bytes] e recuperável com [Ler], para morar num banco
até a hora de baixar.

O fluxo típico:

	a, ok := midia.Extrair(evt.Message)          // no handler: só guardar a.Bytes()
	err := midia.Baixar(ctx, cli, a, destino)     // num worker, fora do handler
	if errors.Is(err, midia.ErrVencido) {
		err = midia.PedirAoCelular(ctx, cli, a, evt.Info)
	}
	// ... e quando chegar *events.MediaRetry:
	novo, err := midia.RespostaDoCelular(retry, a) // novo tem o DirectPath atualizado

Nada disto roda dentro do handler de eventos do whatsmeow: ele processa as
mensagens em série, e um download ali atrasa tudo que vem depois — inclusive a
resposta do celular que o download estaria esperando.

*whatsmeow.Client satisfaz [Baixador] e [Pedinte]; os testes usam falsos.

Estabilidade: v0. Este pacote acompanha a API do whatsmeow, que ainda muda
entre versões; fixe a versão do módulo.

In English: package midia extracts WhatsApp media attachments from whatsmeow
messages into a serializable form, downloads them atomically to disk, and
handles the media-retry flow for expired links. Identifiers and error messages
are in Portuguese, like the rest of whatsapp-reader.
*/
package midia
