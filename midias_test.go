package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/kapstanhq/whatsapp-reader/midia"
)

const joaoJID = "5551999998888@s.whatsapp.net"

func notaDeVozDoJoao() *waE2E.Message {
	um := make([]byte, 32)
	um[0] = 0xab
	return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		Mimetype: proto.String("audio/ogg; codecs=opus"), Seconds: proto.Uint32(42), PTT: proto.Bool(true),
		FileLength: proto.Uint64(51234), FileSHA256: um, FileEncSHA256: um, MediaKey: um,
		DirectPath: proto.String("/v/t62.7117-24/calhas"),
	}}
}

func eventoDe(t *testing.T, conversa, id string, em time.Time, msg *waE2E.Message) *events.Message {
	t.Helper()
	chat := jid(t, conversa)
	return &events.Message{Message: msg, Info: types.MessageInfo{
		ID: id, Timestamp: em, MessageSource: types.MessageSource{Chat: chat, Sender: chat},
	}}
}

func TestPoliticaDoAnexo(t *testing.T) {
	agora := time.Date(2026, 9, 15, 14, 0, 0, 0, time.Local)
	audio, _ := midia.Extrair(notaDeVozDoJoao())
	imagem, _ := midia.Extrair(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{}})

	casos := []struct {
		nome, conversa string
		a              midia.Anexo
		unica          bool
		em             time.Time
		registrar      bool
		estado, motivo string
		chave          bool
	}{
		{"áudio em conversa individual", joaoJID, audio, false, agora, true, midiaPendente, "", true},
		{"áudio em conversa @lid", "152244460753110@lid", audio, false, agora, true, midiaPendente, "", true},
		{"áudio de seis dias", joaoJID, audio, false, agora.AddDate(0, 0, -6), true, midiaPendente, "", true},
		{"áudio de oito dias", joaoJID, audio, false, agora.AddDate(0, 0, -8), true, midiaGuardada, "antigo", true},
		{"áudio em grupo", "120363012345678901@g.us", audio, false, agora, true, midiaGuardada, "grupo", true},
		{"imagem", joaoJID, imagem, false, agora, true, midiaGuardada, "não é áudio", true},
		{"visualização única", joaoJID, audio, true, agora, true, midiaIgnorada, "visualização única", false},
		{"status", "status@broadcast", audio, false, agora, false, "", "", false},
		{"lista de transmissão", "5551999998888@broadcast", audio, false, agora, false, "", "", false},
		{"canal", "120363012345678901@newsletter", audio, false, agora, false, "", "", false},
	}
	for _, c := range casos {
		d := politica(c.conversa, c.a, c.unica, c.em, agora, 7)
		if d.registrar != c.registrar || d.estado != c.estado || d.motivo != c.motivo || d.guardarChave != c.chave {
			t.Errorf("%s: %+v, esperava registrar=%v estado=%q motivo=%q chave=%v",
				c.nome, d, c.registrar, c.estado, c.motivo, c.chave)
		}
	}
}

func TestDiasDeMidia(t *testing.T) {
	casos := map[string]int{"": 7, "30": 30, " 3 ": 3, "0": 7, "-2": 7, "sete": 7}
	for valor, esperado := range casos {
		if n := diasDeMidia(func(string) string { return valor }); n != esperado {
			t.Errorf("WHATSAPP_READER_MIDIA_DIAS=%q deu %d, esperava %d", valor, n, esperado)
		}
	}
}

type linhaMidia struct {
	tipo, estado, sha string
	segundos, origem  int
	voz               bool
	anexo             []byte
	tentativas        int
}

func lerMidia(t *testing.T, b *Banco, id, conversa string) (linhaMidia, bool) {
	t.Helper()
	var l linhaMidia
	err := b.db.QueryRow(`
		SELECT tipo, estado, COALESCE(sha256,''), COALESCE(segundos,0), origem, voz, anexo, tentativas
		  FROM midias WHERE mensagem = ? AND conversa = ?`, id, conversa).
		Scan(&l.tipo, &l.estado, &l.sha, &l.segundos, &l.origem, &l.voz, &l.anexo, &l.tentativas)
	return l, err == nil
}

func TestGravadorGuardaAChaveDoAudioNaHora(t *testing.T) {
	ctx := context.Background()
	b := bancoTeste(t)
	agora := time.Date(2026, 9, 15, 14, 0, 0, 0, time.Local)
	acordou := 0
	g := &gravador{banco: b, dias: 7, agora: func() time.Time { return agora }, acordar: func() { acordou++ }}

	evt := eventoDe(t, joaoJID, "A1", agora, notaDeVozDoJoao())
	g.gravar(ctx, joaoJID, evt, origemAoVivo)

	l, ok := lerMidia(t, b, "A1", joaoJID)
	if !ok {
		t.Fatal("o áudio não foi registrado")
	}
	if l.tipo != "audio" || l.estado != midiaPendente || l.segundos != 42 || !l.voz || l.origem != origemAoVivo {
		t.Errorf("linha errada: %+v", l)
	}
	if !strings.HasPrefix(l.sha, "ab00") {
		t.Errorf("sha256 = %q", l.sha)
	}
	a, err := midia.Ler(midia.Audio, l.anexo)
	if err != nil || a.DirectPath() != "/v/t62.7117-24/calhas" {
		t.Errorf("a chave guardada não reconstrói o anexo: %v %q", err, a.DirectPath())
	}
	if acordou != 1 {
		t.Errorf("a esteira foi acordada %d vezes, esperava 1", acordou)
	}
	// A mensagem em si continua indo para `mensagens`, como sempre.
	var midiaRotulo string
	b.db.QueryRow(`SELECT midia FROM mensagens WHERE id='A1'`).Scan(&midiaRotulo)
	if midiaRotulo != "áudio" {
		t.Errorf("mensagens.midia = %q", midiaRotulo)
	}

	// O history sync repete a mesma mensagem: nada muda, ninguém é acordado.
	g.gravar(ctx, joaoJID, evt, origemHistorico)
	if l2, _ := lerMidia(t, b, "A1", joaoJID); l2.origem != origemAoVivo || l2.estado != midiaPendente {
		t.Errorf("a duplicata mexeu na linha: %+v", l2)
	}
	if acordou != 1 {
		t.Errorf("duplicata acordou a esteira")
	}

	// No meio do download (ou já baixada), a duplicata não desfaz nada.
	for _, estado := range []string{midiaBaixando, midiaBaixada, midiaPedida, midiaIndisponivel} {
		b.db.Exec(`UPDATE midias SET estado = ? WHERE mensagem = 'A1'`, estado)
		g.gravar(ctx, joaoJID, evt, origemHistorico)
		if l3, _ := lerMidia(t, b, "A1", joaoJID); l3.estado != estado {
			t.Errorf("duplicata trocou %s por %s", estado, l3.estado)
		}
	}

	// A que desistiu volta para a fila quando a chave chega de novo.
	b.db.Exec(`UPDATE midias SET estado = 'falhou', tentativas = 6, erro = 'x' WHERE mensagem = 'A1'`)
	g.gravar(ctx, joaoJID, evt, origemHistorico)
	if l4, _ := lerMidia(t, b, "A1", joaoJID); l4.estado != midiaPendente || l4.tentativas != 0 {
		t.Errorf("a linha que falhou não voltou para a fila: %+v", l4)
	}
	if acordou != 2 {
		t.Errorf("a ressurreição devia acordar a esteira (acordou=%d)", acordou)
	}
}

func TestGravadorNaoRegistraTextoNemVisualizacaoUnicaComChave(t *testing.T) {
	ctx := context.Background()
	b := bancoTeste(t)
	agora := time.Now()
	g := &gravador{banco: b, dias: 7, agora: time.Now}

	g.gravar(ctx, joaoJID, eventoDe(t, joaoJID, "T1", agora, &waE2E.Message{Conversation: proto.String("oi")}), origemAoVivo)
	if _, ok := lerMidia(t, b, "T1", joaoJID); ok {
		t.Error("texto não tem anexo e não pode virar linha em midias")
	}

	unica := eventoDe(t, joaoJID, "U1", agora, notaDeVozDoJoao())
	unica.IsViewOnce = true
	g.gravar(ctx, joaoJID, unica, origemAoVivo)
	l, ok := lerMidia(t, b, "U1", joaoJID)
	if !ok || l.estado != midiaIgnorada || l.anexo != nil {
		t.Errorf("visualização única devia ficar ignorada e sem chave: %+v (ok=%v)", l, ok)
	}
}

func TestResumoDosAudiosNoEstado(t *testing.T) {
	ctx := context.Background()
	b := bancoTeste(t)

	if s := b.Saude(ctx).Descrever(); strings.Contains(s, "áudios:") {
		t.Errorf("sem áudio nenhum, o estado não deve ter a linha de áudios:\n%s", s)
	}

	em := time.Now().Unix()
	for _, l := range []struct{ id, estado string }{
		{"P1", midiaPendente}, {"P2", midiaPedida}, {"G1", midiaGuardada}, {"I1", midiaIndisponivel}, {"B1", midiaBaixada},
	} {
		if _, err := b.db.Exec(`INSERT INTO midias (mensagem, conversa, tipo, em, estado) VALUES (?, ?, 'audio', ?, ?)`,
			l.id, joaoJID, em, l.estado); err != nil {
			t.Fatal(err)
		}
	}
	b.db.Exec(`UPDATE midias SET arquivo = 'midia/ab/ab.ogg', tamanho = 1500000 WHERE mensagem = 'B1'`)
	b.db.Exec(`INSERT INTO transcricoes (mensagem, conversa, estado, texto) VALUES ('B1', ?, 'feita', 'olha as calhas')`, joaoJID)
	b.db.Exec(`INSERT INTO midias (mensagem, conversa, tipo, em, estado) VALUES ('IMG', ?, 'imagem', ?, 'guardada')`, joaoJID, em)

	r := b.ResumoAudios(ctx)
	if r.Total != 5 || r.Transcritos != 1 || r.NaFila != 2 || r.SemVolta != 1 || r.NaoBaixados != 1 || r.Bytes != 1500000 {
		t.Errorf("resumo errado: %+v", r)
	}

	s := b.Saude(ctx).Descrever()
	linhas := strings.Split(s, "\n")
	if !strings.HasPrefix(linhas[0], "A PONTE NUNCA SUBIU") {
		t.Errorf("a primeira linha tem que continuar sendo o veredito, veio %q", linhas[0])
	}
	ultima := linhas[len(linhas)-1]
	esperado := "áudios: 1 transcritos · 2 na fila · 1 sem volta · 1 não baixados (grupo, antigo ou visualização única) · 1,4 MB"
	if ultima != esperado {
		t.Errorf("linha de áudios:\n veio %q\n era %q", ultima, esperado)
	}
}

func TestTamanhoHumano(t *testing.T) {
	casos := map[int64]string{1: "1 KB", 1024: "1 KB", 1500000: "1,4 MB", 180 << 20: "180 MB"}
	for n, esperado := range casos {
		if s := tamanhoHumano(n); s != esperado {
			t.Errorf("tamanhoHumano(%d) = %q, esperava %q", n, s, esperado)
		}
	}
}
