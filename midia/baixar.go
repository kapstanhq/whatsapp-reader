package midia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.mau.fi/whatsmeow"
)

// Baixador é o pedaço do *whatsmeow.Client que baixa.
type Baixador interface {
	DownloadToFile(ctx context.Context, msg whatsmeow.DownloadableMessage, file whatsmeow.File) error
}

/* Baixar grava o anexo em destino SEM NUNCA deixar um arquivo pela metade com o
   nome final. O whatsmeow escreve no arquivo à medida que baixa, e num erro o
   que sobra é lixo — cifrado ou incompleto. Então ele escreve em
   destino+".parcial", e só depois de conferido, sincronizado e FECHADO o
   arquivo troca de nome. No Windows o fechar vem antes do renomear por
   obrigação, e o renomear tenta algumas vezes: antivírus costuma segurar
   arquivo recém-escrito por um instante.

   Duas recusas saem antes de gastar a rede: anexo sem hash nunca confere (o
   whatsmeow compara com zeros e falha depois de baixar tudo), e anexo sem
   caminho só o celular resolve. */

func Baixar(ctx context.Context, b Baixador, a Anexo, destino string) error {
	if a.msg == nil {
		return errors.New("midia: anexo vazio")
	}
	if len(a.SHA256()) == 0 {
		return fmt.Errorf("%w: o anexo não traz o hash do conteúdo, e sem ele o download nunca confere", ErrCorrompido)
	}
	if a.DirectPath() == "" {
		return fmt.Errorf("%w: o anexo não traz caminho de download", ErrVencido)
	}
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		return err
	}
	parcial := destino + ".parcial"
	f, err := os.OpenFile(parcial, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := b.DownloadToFile(ctx, a.msg, f); err != nil {
		f.Close()
		os.Remove(parcial)
		return classificar(err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(parcial)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(parcial)
		return err
	}
	if err := renomear(parcial, destino); err != nil {
		os.Remove(parcial)
		return err
	}
	return nil
}

func renomear(de, para string) (err error) {
	for i := 1; i <= 5; i++ {
		if err = os.Rename(de, para); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i) * 100 * time.Millisecond)
	}
	return fmt.Errorf("midia: não consegui pôr o arquivo no lugar (antivírus segurando?): %w", err)
}

// A classificação é a do próprio whatsmeow: 403/404/410 são o link vencido que
// a documentação dele manda resolver pedindo ao celular; os erros de hash e
// HMAC são conteúdo que não confere, e repetir não conserta.
func classificar(err error) error {
	switch {
	case errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith403),
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404),
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410),
		errors.Is(err, whatsmeow.ErrNoURLPresent):
		return fmt.Errorf("%w: %w", ErrVencido, err)
	case errors.Is(err, whatsmeow.ErrInvalidMediaHMAC),
		errors.Is(err, whatsmeow.ErrInvalidMediaEncSHA256),
		errors.Is(err, whatsmeow.ErrInvalidMediaSHA256),
		errors.Is(err, whatsmeow.ErrInvalidUnencryptedMediaSHA256),
		errors.Is(err, whatsmeow.ErrTooShortFile):
		return fmt.Errorf("%w: %w", ErrCorrompido, err)
	}
	return err
}
