package midia

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// Pedinte é o pedaço do *whatsmeow.Client que pede ao celular um link novo.
type Pedinte interface {
	SendMediaRetryReceipt(ctx context.Context, message *types.MessageInfo, mediaKey []byte) error
}

/* O link de um anexo vence em dias. Para o que chegou pelo histórico, ele quase
   sempre já venceu quando se tenta baixar. O caminho é pedir ao CELULAR que
   suba o arquivo de novo: o pedido sai agora, e a resposta chega depois, sozinha,
   como *events.MediaRetry — que não traz a chave do anexo. Quem pediu precisa
   ter guardado o Anexo para abrir a resposta com RespostaDoCelular. */

// PedirAoCelular envia o pedido de reenvio. info precisa de ID e Chat, e em
// grupo também do Sender.
func PedirAoCelular(ctx context.Context, p Pedinte, a Anexo, info types.MessageInfo) error {
	if len(a.Chave()) == 0 {
		return fmt.Errorf("%w: o anexo não traz chave, e sem ela o pedido não pode ser feito", ErrCorrompido)
	}
	if info.ID == "" || info.Chat.IsEmpty() {
		return errors.New("midia: pedir ao celular exige o ID e a conversa da mensagem")
	}
	if info.IsGroup && info.Sender.IsEmpty() {
		return errors.New("midia: em grupo, o pedido ao celular exige o remetente da mensagem")
	}
	return p.SendMediaRetryReceipt(ctx, &info, a.Chave())
}

// RespostaDoCelular abre a resposta com a chave do anexo. Em sucesso devolve
// uma CÓPIA do anexo com o caminho novo; o original não muda.
//
// ErrOutroAnexo quer dizer que a resposta não abre com esta chave: o evento traz
// só o ID da mensagem, e a mesma mensagem pode estar em mais de uma conversa
// (lista de transmissão). Tente o próximo candidato.
func RespostaDoCelular(evt *events.MediaRetry, a Anexo) (Anexo, error) {
	if evt == nil || a.msg == nil {
		return Anexo{}, errors.New("midia: resposta ou anexo vazio")
	}
	notif, err := whatsmeow.DecryptMediaRetryNotification(evt, a.Chave())
	switch {
	case errors.Is(err, whatsmeow.ErrMediaNotAvailableOnPhone):
		return Anexo{}, fmt.Errorf("%w: %w", ErrIndisponivel, err)
	case errors.Is(err, whatsmeow.ErrUnknownMediaRetryError):
		return Anexo{}, err
	case err != nil:
		// A cifra usa a chave do anexo e o ID da mensagem como dado autenticado.
		// Não abrir é não ser deste anexo.
		return Anexo{}, fmt.Errorf("%w: %w", ErrOutroAnexo, err)
	}
	switch notif.GetResult() {
	case waMmsRetry.MediaRetryNotification_SUCCESS:
		if notif.GetDirectPath() == "" {
			return Anexo{}, errors.New("midia: o celular disse que reenviou, mas sem caminho novo")
		}
		return a.comDirectPath(notif.GetDirectPath()), nil
	case waMmsRetry.MediaRetryNotification_NOT_FOUND:
		return Anexo{}, ErrIndisponivel
	case waMmsRetry.MediaRetryNotification_DECRYPTION_ERROR:
		return Anexo{}, fmt.Errorf("%w: o celular não conseguiu decifrar o pedido", ErrCorrompido)
	}
	return Anexo{}, fmt.Errorf("midia: o celular respondeu com erro geral (resultado %d)", notif.GetResult())
}
