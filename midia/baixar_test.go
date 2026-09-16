package midia

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// O falso escreve no arquivo como o whatsmeow faz — aos poucos, e às vezes
// deixa lixo antes de falhar.
type baixadorFalso struct {
	conteudo []byte
	err      error
	chamadas int
}

func (f *baixadorFalso) DownloadToFile(_ context.Context, _ whatsmeow.DownloadableMessage, file whatsmeow.File) error {
	f.chamadas++
	file.Write(f.conteudo)
	return f.err
}

func naoExiste(t *testing.T, caminho string) {
	t.Helper()
	if _, err := os.Stat(caminho); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s não devia existir (stat: %v)", filepath.Base(caminho), err)
	}
}

func TestBaixarPoeOArquivoNoLugarSoNoFim(t *testing.T) {
	a, _ := Extrair(notaDeVoz())
	destino := filepath.Join(t.TempDir(), "ab", "abcdef.ogg")
	f := &baixadorFalso{conteudo: []byte("OggS…")}
	if err := Baixar(context.Background(), f, a, destino); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destino)
	if err != nil || string(got) != "OggS…" {
		t.Errorf("destino = %q, %v", got, err)
	}
	naoExiste(t, destino+".parcial")

	// De novo por cima: o arquivo que já existe é trocado, não dá erro.
	f.conteudo = []byte("OggS novo")
	if err := Baixar(context.Background(), f, a, destino); err != nil {
		t.Fatalf("baixar por cima de arquivo existente: %v", err)
	}
	if got, _ := os.ReadFile(destino); string(got) != "OggS novo" {
		t.Errorf("destino não foi trocado: %q", got)
	}
}

func TestBaixarClassificaOErroENaoDeixaLixo(t *testing.T) {
	casos := []struct {
		nome        string
		err         error
		sentinela   error
		original    error
		transitorio bool
	}{
		{"link vencido 404", whatsmeow.ErrMediaDownloadFailedWith404, ErrVencido, whatsmeow.ErrMediaDownloadFailedWith404, false},
		{"link vencido 410", whatsmeow.ErrMediaDownloadFailedWith410, ErrVencido, whatsmeow.ErrMediaDownloadFailedWith410, false},
		{"HMAC não confere", whatsmeow.ErrInvalidMediaHMAC, ErrCorrompido, whatsmeow.ErrInvalidMediaHMAC, false},
		{"hash não confere", whatsmeow.ErrInvalidMediaSHA256, ErrCorrompido, whatsmeow.ErrInvalidMediaSHA256, false},
		{"rede", errors.New("connection reset by peer"), nil, nil, true},
		{"cancelado", context.Canceled, nil, context.Canceled, true},
	}
	for _, c := range casos {
		a, _ := Extrair(notaDeVoz())
		destino := filepath.Join(t.TempDir(), "x.ogg")
		err := Baixar(context.Background(), &baixadorFalso{conteudo: []byte("lixo cifrado"), err: c.err}, a, destino)
		if err == nil {
			t.Errorf("%s: devia falhar", c.nome)
			continue
		}
		if c.sentinela != nil && !errors.Is(err, c.sentinela) {
			t.Errorf("%s: %v não é %v", c.nome, err, c.sentinela)
		}
		if c.original != nil && !errors.Is(err, c.original) {
			t.Errorf("%s: o erro original do whatsmeow se perdeu: %v", c.nome, err)
		}
		if c.transitorio && (errors.Is(err, ErrVencido) || errors.Is(err, ErrCorrompido)) {
			t.Errorf("%s: erro passageiro foi classificado como definitivo: %v", c.nome, err)
		}
		naoExiste(t, destino)
		naoExiste(t, destino+".parcial")
	}
}

func TestBaixarRecusaAntesDeGastarARede(t *testing.T) {
	semHash := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{DirectPath: proto.String("/v/x"), MediaKey: bytes32(3)}}
	semCaminho := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{FileSHA256: bytes32(1), MediaKey: bytes32(3)}}
	casos := []struct {
		nome      string
		msg       *waE2E.Message
		sentinela error
	}{
		{"sem hash nunca confere", semHash, ErrCorrompido},
		{"sem caminho só o celular resolve", semCaminho, ErrVencido},
	}
	for _, c := range casos {
		a, _ := Extrair(c.msg)
		f := &baixadorFalso{}
		err := Baixar(context.Background(), f, a, filepath.Join(t.TempDir(), "x.ogg"))
		if !errors.Is(err, c.sentinela) {
			t.Errorf("%s: %v, esperava %v", c.nome, err, c.sentinela)
		}
		if f.chamadas != 0 {
			t.Errorf("%s: chamou o download %d vez(es)", c.nome, f.chamadas)
		}
	}
	if err := Baixar(context.Background(), &baixadorFalso{}, Anexo{}, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("anexo vazio tem que ser recusado")
	}
}
