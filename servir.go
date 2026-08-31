package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

/* O daemon. Fica vivo, mantém a conexão e grava. Fechou, o histórico congela
   no último sync — e o que passou enquanto ele estava fora NÃO volta. */

func Servir(dir string) error {
	ctx := context.Background()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	banco, err := AbrirBanco(filepath.Join(dir, "mensagens.db"))
	if err != nil {
		return fmt.Errorf("abrir banco: %w", err)
	}
	defer banco.Fechar()

	// "sqlite" é o nome que o modernc registra. O mattn registrava "sqlite3", e
	// era ele que exigia CGO — e portanto um compilador C na máquina do corretor.
	log := waLog.Stdout("ponte", "INFO", true)
	store, err := sqlstore.New(ctx, "sqlite",
		"file:"+filepath.Join(dir, "sessao.db")+"?_pragma=foreign_keys(1)",
		waLog.Stdout("sessao", "ERROR", true))
	if err != nil {
		return fmt.Errorf("abrir sessão: %w", err)
	}
	device, err := store.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("device: %w", err)
	}

	cli := whatsmeow.NewClient(device, log)
	cli.AddEventHandler(func(bruto any) {
		switch e := bruto.(type) {

		case *events.Message:
			gravarUma(ctx, banco, e.Info.ID, e.Info.Chat.String(),
				e.Info.Sender.String(), e.Info.PushName, e.Info.IsFromMe,
				e.Info.Timestamp, e.Message)

		case *events.HistorySync:
			n := 0
			for _, conv := range e.Data.GetConversations() {
				jid := conv.GetID()
				for _, hm := range conv.GetMessages() {
					wm := hm.GetMessage()
					if wm == nil {
						continue
					}
					k := wm.GetKey()
					gravarUma(ctx, banco, k.GetID(), jid, k.GetParticipant(),
						wm.GetPushName(), k.GetFromMe(),
						time.Unix(int64(wm.GetMessageTimestamp()), 0),
						wm.GetMessage())
					n++
				}
			}
			c, m := banco.Contagem(ctx)
			fmt.Printf("· histórico: +%d mensagens (total %d em %d conversas)\n", n, m, c)

		case *events.Connected:
			fmt.Println("· conectado")

		case *events.ClientOutdated:
			// O erro que custou três tentativas na primeira instalação vinha
			// mudo, como `websocket: close 1006`. Aqui ele tem nome.
			fmt.Println("!! cliente desatualizado — o WhatsApp recusou esta versão.")
			fmt.Println("   conserto: go get -u go.mau.fi/whatsmeow@latest e recompilar")

		case *events.LoggedOut:
			fmt.Println("!! sessão encerrada no celular — rode `serve` de novo para parear")
		}
	})

	if cli.Store.ID == nil {
		if err := parear(ctx, cli, dir); err != nil {
			return err
		}
	} else if err := cli.Connect(); err != nil {
		return fmt.Errorf("conectar: %w", err)
	}

	elo, err := AbrirElo(dir, cli, banco)
	if err != nil {
		return fmt.Errorf("elo: %w", err)
	}
	defer elo.Fechar()

	c, m := banco.Contagem(ctx)
	fmt.Printf("· ponte de pé · %d conversas, %d mensagens · %s\n", c, m, dir)
	fmt.Println("· deixe esta janela aberta. Ctrl+C encerra.")

	parar := make(chan os.Signal, 1)
	signal.Notify(parar, os.Interrupt, syscall.SIGTERM)
	<-parar
	cli.Disconnect()
	fmt.Println("· encerrada")
	return nil
}

func parear(ctx context.Context, cli *whatsmeow.Client, dir string) error {
	canal, _ := cli.GetQRChannel(ctx)
	if err := cli.Connect(); err != nil {
		return fmt.Errorf("conectar: %w", err)
	}
	png := filepath.Join(dir, "qr.png")
	for evt := range canal {
		switch evt.Event {
		case "code":
			// O QR vai para os dois lugares porque nenhum dos dois é confiável
			// sozinho: o terminal do Windows nem sempre desenha meio-bloco, e o
			// PNG não serve para quem roda sem interface gráfica.
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			if err := qrcode.WriteFile(evt.Code, qrcode.Medium, 512, png); err == nil {
				fmt.Printf("\n   também em imagem: %s\n", png)
			}
			fmt.Println("   celular · Configurações › Dispositivos conectados › Conectar")
		case "success":
			os.Remove(png) // o QR é credencial: some assim que serve
			fmt.Println("· pareado")
			return nil
		case "timeout":
			return fmt.Errorf("o QR expirou sem ser lido")
		}
	}
	return nil
}

// O texto mora em lugares diferentes conforme o tipo. Sem isto, toda mensagem
// com legenda ou resposta entra no banco vazia.
func textoDe(m *waE2E.Message) (string, string) {
	if m == nil {
		return "", ""
	}
	switch {
	case m.GetConversation() != "":
		return m.GetConversation(), ""
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetText(), ""
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetCaption(), "imagem"
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetCaption(), "vídeo"
	case m.GetDocumentMessage() != nil:
		return m.GetDocumentMessage().GetFileName(), "documento"
	case m.GetAudioMessage() != nil:
		return "", "áudio"
	case m.GetStickerMessage() != nil:
		return "", "figurinha"
	}
	return "", ""
}

func gravarUma(ctx context.Context, b *Banco, id, conversa, remetente, nome string,
	deMim bool, em time.Time, msg *waE2E.Message) {
	if id == "" || conversa == "" {
		return
	}
	texto, midia := textoDe(msg)
	if texto == "" && midia == "" {
		return // recibo, reação, edição de estado: não é conversa
	}
	_ = b.GravarConversa(ctx, conversa, nome, em)
	_ = b.GravarMensagem(ctx, Mensagem{
		ID: id, Conversa: conversa, Remetente: remetente,
		DeMim: deMim, Texto: texto, Midia: midia, Em: em,
	})
}
