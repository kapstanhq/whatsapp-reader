package midia

import (
	"bytes"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func bytes32(n byte) []byte { return bytes.Repeat([]byte{n}, 32) }

var citada = &waE2E.ContextInfo{QuotedMessage: &waE2E.Message{Conversation: proto.String("a mensagem citada, que pode ser enorme")}}

func notaDeVoz() *waE2E.Message {
	return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		Mimetype: proto.String("audio/ogg; codecs=opus"), Seconds: proto.Uint32(42), PTT: proto.Bool(true),
		FileLength: proto.Uint64(51234), FileSHA256: bytes32(1), FileEncSHA256: bytes32(2), MediaKey: bytes32(3),
		DirectPath: proto.String("/v/t62.7117-24/nota"), ContextInfo: citada,
	}}
}

func TestExtrairPorTipo(t *testing.T) {
	casos := []struct {
		nome     string
		msg      *waE2E.Message
		ok       bool
		tipo     Tipo
		mime     string
		segundos uint32
		voz      bool
		ext      string
	}{
		{"nota de voz", notaDeVoz(), true, Audio, "audio/ogg; codecs=opus", 42, true, ".ogg"},
		{"imagem", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Mimetype: proto.String("image/jpeg"), JPEGThumbnail: []byte("miniatura"), ContextInfo: citada,
			FileSHA256: bytes32(1), DirectPath: proto.String("/i")}}, true, Imagem, "image/jpeg", 0, false, ".jpg"},
		{"vídeo", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Mimetype: proto.String("video/mp4"), Seconds: proto.Uint32(9), JPEGThumbnail: []byte("miniatura")}},
			true, Video, "video/mp4", 9, false, ".mp4"},
		{"documento", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			Mimetype: proto.String("application/octet-stream"), FileName: proto.String("Contrato.PDF")}},
			true, Documento, "application/octet-stream", 0, false, ".pdf"},
		{"texto não tem anexo", &waE2E.Message{Conversation: proto.String("oi")}, false, "", "", 0, false, ""},
		{"figurinha fica de fora", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}}, false, "", "", 0, false, ""},
		{"mensagem nula", nil, false, "", "", 0, false, ""},
	}
	for _, c := range casos {
		a, ok := Extrair(c.msg)
		if ok != c.ok {
			t.Errorf("%s: ok=%v, esperava %v", c.nome, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if a.Tipo != c.tipo || a.Mime() != c.mime || a.Segundos() != c.segundos || a.Voz() != c.voz || a.Extensao() != c.ext {
			t.Errorf("%s: tipo=%s mime=%q segundos=%d voz=%v ext=%q", c.nome, a.Tipo, a.Mime(), a.Segundos(), a.Voz(), a.Extensao())
		}
	}
}

func TestExtrairTiraMiniaturaECitacaoSemMexerNoOriginal(t *testing.T) {
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{JPEGThumbnail: []byte("miniatura"), ContextInfo: citada}}
	a, _ := Extrair(msg)
	img := a.Proto().(*waE2E.ImageMessage)
	if img.JPEGThumbnail != nil || img.ContextInfo != nil {
		t.Error("o anexo guardado não pode levar miniatura nem mensagem citada")
	}
	if msg.GetImageMessage().GetJPEGThumbnail() == nil || msg.GetImageMessage().GetContextInfo() == nil {
		t.Error("Extrair mexeu na mensagem original")
	}
}

func TestAnexoIdaEVoltaPeloBanco(t *testing.T) {
	a, _ := Extrair(notaDeVoz())
	dados, err := a.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Ler(Audio, dados)
	if err != nil {
		t.Fatal(err)
	}
	if b.DirectPath() != a.DirectPath() || !bytes.Equal(b.Chave(), a.Chave()) ||
		!bytes.Equal(b.SHA256(), a.SHA256()) || b.Tamanho() != 51234 || !b.Voz() {
		t.Errorf("o anexo voltou diferente: path=%q tamanho=%d voz=%v", b.DirectPath(), b.Tamanho(), b.Voz())
	}
	if _, err := Ler("figurinha", dados); err == nil {
		t.Error("tipo desconhecido tem que ser recusado")
	}
	if _, err := Ler(Audio, []byte{0xff, 0xff, 0xff}); err == nil {
		t.Error("bytes ilegíveis têm que ser recusados")
	}
}

func TestExtensaoNaoConfiaNoNomeDoRemetente(t *testing.T) {
	doc := func(nome, mime string) Anexo {
		a, _ := Extrair(&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			FileName: proto.String(nome), Mimetype: proto.String(mime)}})
		return a
	}
	casos := []struct {
		nome string
		a    Anexo
		ext  string
	}{
		{"pdf pelo nome", doc("laudo.pdf", ""), ".pdf"},
		{"nome com separador cai no mime", doc(`..\..\x.sh;rm`, "application/pdf"), ".pdf"},
		{"nome sem extensão e mime desconhecido", doc("arquivo", "application/x-coisa"), ".bin"},
		{"extensão comprida demais", doc("a.extensaolonga", ""), ".bin"},
		{"anexo vazio", Anexo{}, ".bin"},
	}
	for _, c := range casos {
		if e := c.a.Extensao(); e != c.ext {
			t.Errorf("%s: %q, esperava %q", c.nome, e, c.ext)
		}
	}
}

func TestAnexoVazioNaoEntraEmPanico(t *testing.T) {
	var a Anexo
	if a.Mime() != "" || a.Segundos() != 0 || a.Voz() || a.Tamanho() != 0 ||
		a.SHA256() != nil || a.Chave() != nil || a.DirectPath() != "" || a.NomeDoArquivo() != "" {
		t.Error("anexo vazio devia devolver zeros")
	}
	if _, err := a.Bytes(); err == nil {
		t.Error("anexo vazio não serializa")
	}
}
