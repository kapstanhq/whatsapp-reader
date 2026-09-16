package midia

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// Tipo é a família do anexo. O valor é o que se grava no banco.
type Tipo string

const (
	Audio     Tipo = "audio"
	Imagem    Tipo = "imagem"
	Video     Tipo = "video"
	Documento Tipo = "documento"
)

// Os quatro desfechos que mudam o que fazer em seguida. Os erros devolvidos
// pelo pacote os embrulham junto com o erro original do whatsmeow: tanto
// errors.Is(err, ErrVencido) quanto errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404)
// valem.
var (
	// O link do anexo venceu (403/404/410) ou nunca veio. Só o celular pode
	// reenviar: ver PedirAoCelular.
	ErrVencido = errors.New("midia: o link do anexo venceu; só o celular pode reenviar")
	// O que chegou não confere com a chave ou com o hash. Tentar de novo não muda nada.
	ErrCorrompido = errors.New("midia: o arquivo não confere com a chave do anexo")
	// O celular respondeu que não tem mais o arquivo.
	ErrIndisponivel = errors.New("midia: o celular não tem mais este arquivo")
	// A resposta do celular não abre com a chave deste anexo: é de outro.
	ErrOutroAnexo = errors.New("midia: a resposta do celular não é deste anexo")
)

type protoBaixavel interface {
	whatsmeow.DownloadableMessage
	proto.Message
}

// Anexo é o proto de um anexo, pronto para ser guardado e baixado depois.
type Anexo struct {
	Tipo Tipo
	msg  protoBaixavel
}

/* Extrair tira o anexo da mensagem JÁ DESEMBRULHADA (events.Message.Message, ou
   depois de UnwrapRaw). A cópia sai sem miniatura e sem ContextInfo: a mensagem
   citada mora no ContextInfo e pode ser do tamanho de outra mensagem inteira,
   e nenhum dos dois serve para baixar. A mensagem original não é tocada. */

func Extrair(m *waE2E.Message) (Anexo, bool) {
	switch {
	case m.GetAudioMessage() != nil:
		c := proto.Clone(m.GetAudioMessage()).(*waE2E.AudioMessage)
		c.ContextInfo = nil
		return Anexo{Tipo: Audio, msg: c}, true
	case m.GetImageMessage() != nil:
		c := proto.Clone(m.GetImageMessage()).(*waE2E.ImageMessage)
		c.ContextInfo, c.JPEGThumbnail = nil, nil
		return Anexo{Tipo: Imagem, msg: c}, true
	case m.GetVideoMessage() != nil:
		c := proto.Clone(m.GetVideoMessage()).(*waE2E.VideoMessage)
		c.ContextInfo, c.JPEGThumbnail = nil, nil
		return Anexo{Tipo: Video, msg: c}, true
	case m.GetDocumentMessage() != nil:
		c := proto.Clone(m.GetDocumentMessage()).(*waE2E.DocumentMessage)
		c.ContextInfo, c.JPEGThumbnail = nil, nil
		return Anexo{Tipo: Documento, msg: c}, true
	}
	return Anexo{}, false
}

// Ler reconstrói um Anexo a partir do que Bytes devolveu.
func Ler(t Tipo, dados []byte) (Anexo, error) {
	var msg protoBaixavel
	switch t {
	case Audio:
		msg = &waE2E.AudioMessage{}
	case Imagem:
		msg = &waE2E.ImageMessage{}
	case Video:
		msg = &waE2E.VideoMessage{}
	case Documento:
		msg = &waE2E.DocumentMessage{}
	default:
		return Anexo{}, fmt.Errorf("midia: tipo desconhecido %q", t)
	}
	if err := proto.Unmarshal(dados, msg); err != nil {
		return Anexo{}, fmt.Errorf("midia: anexo ilegível: %w", err)
	}
	return Anexo{Tipo: t, msg: msg}, nil
}

// Bytes serializa o anexo para guardar.
func (a Anexo) Bytes() ([]byte, error) {
	if a.msg == nil {
		return nil, errors.New("midia: anexo vazio")
	}
	return proto.Marshal(a.msg)
}

// Proto devolve o proto do anexo, para quem precisar chamar o whatsmeow direto.
func (a Anexo) Proto() whatsmeow.DownloadableMessage { return a.msg }

func (a Anexo) Mime() string {
	if m, ok := a.msg.(interface{ GetMimetype() string }); ok {
		return m.GetMimetype()
	}
	return ""
}

// Segundos de áudio ou vídeo; 0 para os outros, ou quando o remetente não informou.
func (a Anexo) Segundos() uint32 {
	if m, ok := a.msg.(interface{ GetSeconds() uint32 }); ok {
		return m.GetSeconds()
	}
	return 0
}

// Voz diz se o áudio foi gravado no botão do microfone (e não enviado como arquivo).
func (a Anexo) Voz() bool {
	if m, ok := a.msg.(interface{ GetPTT() bool }); ok {
		return m.GetPTT()
	}
	return false
}

func (a Anexo) Tamanho() uint64 {
	if m, ok := a.msg.(interface{ GetFileLength() uint64 }); ok {
		return m.GetFileLength()
	}
	return 0
}

// SHA256 do conteúdo decifrado: identidade do arquivo, igual em encaminhamentos.
func (a Anexo) SHA256() []byte {
	if a.msg == nil {
		return nil
	}
	return a.msg.GetFileSHA256()
}

// Chave de mídia: decifra o download e a resposta do celular.
func (a Anexo) Chave() []byte {
	if a.msg == nil {
		return nil
	}
	return a.msg.GetMediaKey()
}

func (a Anexo) DirectPath() string {
	if a.msg == nil {
		return ""
	}
	return a.msg.GetDirectPath()
}

// NomeDoArquivo é o nome que o remetente deu ao documento. Vem de fora: não
// use como caminho.
func (a Anexo) NomeDoArquivo() string {
	if m, ok := a.msg.(interface{ GetFileName() string }); ok {
		return m.GetFileName()
	}
	return ""
}

var extensoes = map[string]string{
	"audio/ogg": ".ogg", "audio/opus": ".opus", "audio/mpeg": ".mp3", "audio/mp4": ".m4a",
	"audio/aac": ".aac", "audio/amr": ".amr", "audio/wav": ".wav", "audio/x-wav": ".wav",
	"audio/webm": ".webm",
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif",
	"video/mp4": ".mp4", "video/3gpp": ".3gp", "video/quicktime": ".mov",
	"application/pdf": ".pdf",
}

// Extensao para gravar o arquivo. Tabela própria, e não o pacote mime: no
// Windows ele consulta o registro, e "audio/ogg" vira o que o último programa
// instalado quis.
func (a Anexo) Extensao() string {
	if a.Tipo == Documento {
		if e := extensaoSegura(filepath.Ext(a.NomeDoArquivo())); e != "" {
			return e
		}
	}
	base, _, _ := strings.Cut(a.Mime(), ";")
	if e, ok := extensoes[strings.ToLower(strings.TrimSpace(base))]; ok {
		return e
	}
	return ".bin"
}

// O nome do documento é escolhido por quem mandou. Da extensão só passa letra
// e número, curta — nada que vire separador de caminho ou comando.
func extensaoSegura(e string) string {
	e = strings.ToLower(e)
	if len(e) < 2 || len(e) > 9 {
		return ""
	}
	for _, r := range e[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return e
}

func (a Anexo) comDirectPath(p string) Anexo {
	c := proto.Clone(a.msg).(protoBaixavel)
	switch m := c.(type) {
	case *waE2E.AudioMessage:
		m.DirectPath = proto.String(p)
	case *waE2E.ImageMessage:
		m.DirectPath = proto.String(p)
	case *waE2E.VideoMessage:
		m.DirectPath = proto.String(p)
	case *waE2E.DocumentMessage:
		m.DirectPath = proto.String(p)
	}
	return Anexo{Tipo: a.Tipo, msg: c}
}
