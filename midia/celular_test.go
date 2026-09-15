package midia

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/gcmutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"
	"google.golang.org/protobuf/proto"
)

/* A resposta do celular é cifrada de verdade aqui, com a mesma derivação do
   whatsmeow: HKDF da chave de mídia, AES-GCM com o ID da mensagem como dado
   autenticado. Um falso que pulasse a cifra não pegaria a resposta aberta com
   a chave errada — que é justamente o caso do ErrOutroAnexo. */

func respostaCifrada(t *testing.T, chave []byte, id string, notif *waMmsRetry.MediaRetryNotification) *events.MediaRetry {
	t.Helper()
	plano, err := proto.Marshal(notif)
	if err != nil {
		t.Fatal(err)
	}
	k := hkdfutil.SHA256(chave, nil, []byte("WhatsApp Media Retry Notification"), 32)
	iv := make([]byte, 12)
	rand.Read(iv)
	cifrado, err := gcmutil.Encrypt(k, iv, plano, []byte(id))
	if err != nil {
		t.Fatal(err)
	}
	return &events.MediaRetry{Ciphertext: cifrado, IV: iv, MessageID: types.MessageID(id)}
}

func TestRespostaDoCelular(t *testing.T) {
	a, _ := Extrair(notaDeVoz())
	sucesso := &waMmsRetry.MediaRetryNotification{StanzaID: proto.String("M1"),
		DirectPath: proto.String("/v/t62.7117-24/novo"), Result: waMmsRetry.MediaRetryNotification_SUCCESS.Enum()}

	t.Run("sucesso troca o caminho numa cópia", func(t *testing.T) {
		novo, err := RespostaDoCelular(respostaCifrada(t, a.Chave(), "M1", sucesso), a)
		if err != nil {
			t.Fatal(err)
		}
		if novo.DirectPath() != "/v/t62.7117-24/novo" {
			t.Errorf("caminho novo = %q", novo.DirectPath())
		}
		if a.DirectPath() != "/v/t62.7117-24/nota" {
			t.Error("o anexo original mudou")
		}
		if string(novo.Chave()) != string(a.Chave()) || novo.Segundos() != 42 || novo.Tipo != Audio {
			t.Error("a cópia perdeu o resto do anexo")
		}
	})

	t.Run("resposta aberta com a chave de outro anexo", func(t *testing.T) {
		_, err := RespostaDoCelular(respostaCifrada(t, bytes32(9), "M1", sucesso), a)
		if !errors.Is(err, ErrOutroAnexo) {
			t.Errorf("%v, esperava ErrOutroAnexo", err)
		}
	})

	t.Run("ID de outra mensagem também não abre", func(t *testing.T) {
		evt := respostaCifrada(t, a.Chave(), "M1", sucesso)
		evt.MessageID = "M2"
		if _, err := RespostaDoCelular(evt, a); !errors.Is(err, ErrOutroAnexo) {
			t.Errorf("%v, esperava ErrOutroAnexo", err)
		}
	})

	t.Run("celular não achou", func(t *testing.T) {
		n := &waMmsRetry.MediaRetryNotification{Result: waMmsRetry.MediaRetryNotification_NOT_FOUND.Enum()}
		if _, err := RespostaDoCelular(respostaCifrada(t, a.Chave(), "M1", n), a); !errors.Is(err, ErrIndisponivel) {
			t.Errorf("%v, esperava ErrIndisponivel", err)
		}
	})

	t.Run("erro em claro código 2", func(t *testing.T) {
		evt := &events.MediaRetry{MessageID: "M1", Error: &events.MediaRetryError{Code: 2}}
		if _, err := RespostaDoCelular(evt, a); !errors.Is(err, ErrIndisponivel) {
			t.Errorf("%v, esperava ErrIndisponivel", err)
		}
	})

	t.Run("celular não decifrou o pedido", func(t *testing.T) {
		n := &waMmsRetry.MediaRetryNotification{Result: waMmsRetry.MediaRetryNotification_DECRYPTION_ERROR.Enum()}
		if _, err := RespostaDoCelular(respostaCifrada(t, a.Chave(), "M1", n), a); !errors.Is(err, ErrCorrompido) {
			t.Errorf("%v, esperava ErrCorrompido", err)
		}
	})

	t.Run("erro geral é passageiro", func(t *testing.T) {
		n := &waMmsRetry.MediaRetryNotification{Result: waMmsRetry.MediaRetryNotification_GENERAL_ERROR.Enum()}
		_, err := RespostaDoCelular(respostaCifrada(t, a.Chave(), "M1", n), a)
		if err == nil || errors.Is(err, ErrIndisponivel) || errors.Is(err, ErrCorrompido) || errors.Is(err, ErrOutroAnexo) {
			t.Errorf("erro geral devia ser passageiro, veio %v", err)
		}
	})

	t.Run("sucesso sem caminho", func(t *testing.T) {
		n := &waMmsRetry.MediaRetryNotification{Result: waMmsRetry.MediaRetryNotification_SUCCESS.Enum()}
		if _, err := RespostaDoCelular(respostaCifrada(t, a.Chave(), "M1", n), a); err == nil {
			t.Error("sucesso sem caminho não pode virar anexo")
		}
	})

	t.Run("resposta nula", func(t *testing.T) {
		if _, err := RespostaDoCelular(nil, a); err == nil {
			t.Error("resposta nula tem que ser recusada")
		}
	})
}

type pedinteFalso struct {
	info     *types.MessageInfo
	chave    []byte
	chamadas int
}

func (p *pedinteFalso) SendMediaRetryReceipt(_ context.Context, info *types.MessageInfo, chave []byte) error {
	p.chamadas++
	p.info, p.chave = info, chave
	return nil
}

func TestPedirAoCelular(t *testing.T) {
	ctx := context.Background()
	a, _ := Extrair(notaDeVoz())
	joao, _ := types.ParseJID("5551999998888@s.whatsapp.net")
	grupo, _ := types.ParseJID("120363012345678901@g.us")

	p := &pedinteFalso{}
	info := types.MessageInfo{ID: "M1", MessageSource: types.MessageSource{Chat: joao, Sender: joao}}
	if err := PedirAoCelular(ctx, p, a, info); err != nil {
		t.Fatal(err)
	}
	if p.chamadas != 1 || p.info.ID != "M1" || string(p.chave) != string(a.Chave()) {
		t.Errorf("o pedido não levou a mensagem e a chave: %+v", p)
	}

	semChave, _ := Extrair(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		FileSHA256: bytes32(1), DirectPath: proto.String("/v/x")}})
	casos := []struct {
		nome string
		a    Anexo
		info types.MessageInfo
	}{
		{"anexo sem chave", semChave, info},
		{"sem ID", a, types.MessageInfo{MessageSource: types.MessageSource{Chat: joao}}},
		{"grupo sem remetente", a, types.MessageInfo{ID: "G1", MessageSource: types.MessageSource{Chat: grupo, IsGroup: true}}},
	}
	for _, c := range casos {
		p := &pedinteFalso{}
		if err := PedirAoCelular(ctx, p, c.a, c.info); err == nil || p.chamadas != 0 {
			t.Errorf("%s: devia recusar sem pedir (err=%v, chamadas=%d)", c.nome, err, p.chamadas)
		}
	}
	semChaveErr := PedirAoCelular(ctx, &pedinteFalso{}, semChave, info)
	if !errors.Is(semChaveErr, ErrCorrompido) {
		t.Errorf("anexo sem chave devia ser ErrCorrompido, veio %v", semChaveErr)
	}
}
