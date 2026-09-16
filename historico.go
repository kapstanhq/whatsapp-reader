package main

import (
	"time"

	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

/* O HISTÓRICO e as mensagens ao vivo chegam em formatos diferentes, e só um
   deles vinha desembrulhado.

   Ao vivo, o whatsmeow entrega `events.Message` com o conteúdo já tirado da
   casca: conversa temporária (EphemeralMessage), visualização única, edição.
   No history sync a mensagem vem crua, ainda dentro dessas cascas — e o
   `textoDe`, olhando só a casca, devolvia vazio. Como `gravarUma` descarta o
   que não tem texto nem mídia, toda mensagem de conversa temporária que chegou
   pelo histórico sumia em silêncio, texto e áudio. Achado lendo o código em
   15/09/2026, ao planejar a transcrição de áudio.

   `ParseWebMessage` é o caminho do próprio whatsmeow para deixar o histórico
   igual ao ao vivo: desembrulha e monta o remetente. Ele recusa dois casos que
   acontecem de verdade — mensagem minha antes de a sessão saber o próprio
   número, e grupo sem participante — e aí a mensagem não pode se perder: cai
   no desembrulho direto, com o que a chave tiver. */

type interpretador interface {
	ParseWebMessage(types.JID, *waWeb.WebMessageInfo) (*events.Message, error)
}

func mensagemDoHistorico(cli interpretador, conversa string, wm *waWeb.WebMessageInfo) *events.Message {
	if chat, err := types.ParseJID(conversa); err == nil && !chat.IsEmpty() {
		if evt, err := cli.ParseWebMessage(chat, wm); err == nil {
			return evt
		}
	}
	k := wm.GetKey()
	evt := &events.Message{
		RawMessage:   wm.GetMessage(),
		SourceWebMsg: wm,
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{IsFromMe: k.GetFromMe()},
			ID:            k.GetID(),
			PushName:      wm.GetPushName(),
			Timestamp:     time.Unix(int64(wm.GetMessageTimestamp()), 0),
		},
	}
	if p := k.GetParticipant(); p != "" {
		if s, err := types.ParseJID(p); err == nil {
			evt.Info.Sender = s
		}
	}
	// A conversa que se grava continua sendo a string que o sync mandou, e não
	// o JID interpretado: é com ela que as linhas antigas já estão no banco.
	return evt.UnwrapRaw()
}
