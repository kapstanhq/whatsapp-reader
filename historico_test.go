package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

/* O que se confere aqui é a mensagem EMBRULHADA do histórico chegando ao banco,
   pelos dois caminhos: o do `ParseWebMessage` e o do desembrulho direto, quando
   ele recusa. O cliente é real e nunca conecta — é justamente o estado em que o
   history sync da primeira pareada ainda não sabe o próprio número. */

func clienteSemConexao(t *testing.T) *whatsmeow.Client {
	t.Helper()
	c, err := sqlstore.New(context.Background(), "sqlite", dsnSessao(t.TempDir()), waLog.Noop)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return whatsmeow.NewClient(c.NewDevice(), waLog.Noop)
}

func bancoTeste(t *testing.T) *Banco {
	t.Helper()
	b, err := AbrirBanco(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Fechar() })
	return b
}

func doHistorico(conversa, id, participante string, deMim bool, em time.Time, msg *waE2E.Message) *waWeb.WebMessageInfo {
	k := &waCommon.MessageKey{RemoteJID: proto.String(conversa), FromMe: proto.Bool(deMim), ID: proto.String(id)}
	if participante != "" {
		k.Participant = proto.String(participante)
	}
	return &waWeb.WebMessageInfo{Key: k, MessageTimestamp: proto.Uint64(uint64(em.Unix())), Message: msg}
}

func temporaria(m *waE2E.Message) *waE2E.Message {
	return &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: m}}
}

func TestHistoricoGravaMensagemDeConversaTemporaria(t *testing.T) {
	ctx := context.Background()
	b := bancoTeste(t)
	cli := clienteSemConexao(t)
	em := time.Date(2026, 6, 25, 9, 9, 0, 0, time.Local)
	const joao = "5551999998888@s.whatsapp.net"
	const grupo = "120363012345678901@g.us"

	casos := []struct {
		nome, conversa, id, participante string
		deMim                            bool
		msg                              *waE2E.Message
		texto, midia, remetente          string
	}{
		{"texto de conversa temporária", joao, "T1", "", false,
			temporaria(&waE2E.Message{Conversation: proto.String("amanhã passo aí pra ver as calhas")}),
			"amanhã passo aí pra ver as calhas", "", joao},
		{"áudio de conversa temporária", joao, "A1", "", false,
			temporaria(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{Seconds: proto.Uint32(42), PTT: proto.Bool(true)}}),
			"", "áudio", joao},
		{"minha, antes de a sessão saber o número: desembrulho direto", joao, "M1", "", true,
			temporaria(&waE2E.Message{Conversation: proto.String("combinado")}),
			"combinado", "", ""},
		{"grupo com participante", grupo, "G1", "5551888887777@s.whatsapp.net", false,
			temporaria(&waE2E.Message{Conversation: proto.String("bom dia")}),
			"bom dia", "", "5551888887777@s.whatsapp.net"},
		{"grupo sem participante: desembrulho direto", grupo, "G2", "", false,
			temporaria(&waE2E.Message{Conversation: proto.String("boa tarde")}),
			"boa tarde", "", ""},
		{"sem casca nenhuma continua igual", joao, "S1", "", false,
			&waE2E.Message{Conversation: proto.String("oi")},
			"oi", "", joao},
	}
	for _, c := range casos {
		wm := doHistorico(c.conversa, c.id, c.participante, c.deMim, em, c.msg)
		g := &gravador{banco: b, dias: 7, agora: func() time.Time { return em }}
		g.gravar(ctx, c.conversa, mensagemDoHistorico(cli, c.conversa, wm), origemHistorico)

		var texto, midia, remetente string
		var deMim bool
		var seg int64
		err := b.db.QueryRowContext(ctx, `
			SELECT COALESCE(texto,''), COALESCE(midia,''), COALESCE(remetente,''), de_mim, em
			  FROM mensagens WHERE id = ? AND conversa = ?`, c.id, c.conversa).
			Scan(&texto, &midia, &remetente, &deMim, &seg)
		if err != nil {
			t.Errorf("%s: a mensagem não chegou ao banco (%v)", c.nome, err)
			continue
		}
		if texto != c.texto || midia != c.midia {
			t.Errorf("%s: gravou texto=%q midia=%q, esperava texto=%q midia=%q",
				c.nome, texto, midia, c.texto, c.midia)
		}
		if remetente != c.remetente {
			t.Errorf("%s: remetente=%q, esperava %q", c.nome, remetente, c.remetente)
		}
		if deMim != c.deMim {
			t.Errorf("%s: de_mim=%v, esperava %v", c.nome, deMim, c.deMim)
		}
		if seg != em.Unix() {
			t.Errorf("%s: em=%d, esperava %d", c.nome, seg, em.Unix())
		}
	}
}
