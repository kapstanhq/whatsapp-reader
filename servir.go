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
	store, err := sqlstore.New(ctx, "sqlite", dsnSessao(dir), waLog.Stdout("sessao", "ERROR", true))
	if err != nil {
		return erroSessao(err)
	}
	// Fechar faz o checkpoint do WAL: sem isto o que ficou no -wal só volta ao
	// arquivo principal na próxima abertura.
	defer store.Close()
	device, err := store.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("device: %w", err)
	}

	cli := whatsmeow.NewClient(device, log)
	// A chave de cada anexo é guardada no handler, sem rede; quem baixa é a
	// esteira, fora dele. Ver midias.go e esteira.go.
	g := &gravador{banco: banco, dias: diasDeMidia(os.Getenv), agora: time.Now}
	// O motor sai do ambiente desta janela, e o que ele é vai para o banco: o
	// `mcp` roda com outro ambiente. Ver motor.go.
	mt := montarMotor(dir, os.Getenv)
	banco.Anotar(ctx, "transcricao_motor", mt.descricao)
	banco.Anotar(ctx, "transcricao_problema", mt.problema)
	fmt.Println("· transcrição:", mt.descricao)
	cfg := configEsteiraPadrao(g.dias)
	cfg.motor, cfg.idioma = mt.motor, mt.idioma
	cfg.guardarDias = diasDeGuarda(os.Getenv)
	esteira, err := AbrirEsteira(ctx, dir, banco, cli, cfg)
	if err != nil {
		return err
	}
	defer esteira.Fechar()
	g.acordar = esteira.Acordar
	cli.AddEventHandler(func(bruto any) {
		switch e := bruto.(type) {

		case *events.Message:
			g.gravar(ctx, e.Info.Chat.String(), e, origemAoVivo)

		case *events.MediaRetry:
			// O celular respondeu ao pedido de link novo. Só banco aqui; baixar é da esteira.
			esteira.RespostaDoCelular(ctx, e)

		case *events.HistorySync:
			n := 0
			for _, conv := range e.Data.GetConversations() {
				jid := conv.GetID()
				for _, hm := range conv.GetMessages() {
					if wm := hm.GetMessage(); wm != nil {
						// O histórico vem embrulhado; o ao vivo, não. Ver historico.go.
						g.gravar(ctx, jid, mensagemDoHistorico(cli, jid, wm), origemHistorico)
						n++
					}
				}
			}
			c, m := banco.Contagem(ctx)
			fmt.Printf("· histórico: +%d mensagens (total %d em %d conversas)\n", n, m, c)

		/* Os eventos de CONEXÃO, e a razão de serem tantos: um daemon vivo e
		   mudo é indistinguível de um daemon vivo e trabalhando. Sem estes
		   casos o processo fica de pé sem receber nada e ninguém sabe — foi o
		   que aconteceu no 01/09, e custou seis dias de conversa. Cada um anota
		   no banco, e é de lá que o `estado_da_ponte` tira o veredito.

		   A divisão que importa não é entre os eventos: é entre o que VOLTA
		   sozinho (queda de rede, keepalive) e o que EXIGE gente (sessão
		   assumida, deslogada, versão velha, banimento). O texto de cada um diz
		   de qual dos dois se trata, porque é a única coisa que quem lê precisa
		   decidir. */

		case *events.Connected:
			// O número entra aqui, e não no start: antes de conectar, o
			// Store.ID existe sem a sessão valer. É ele que o degrau do envio
			// de teste usa como destinatário.
			if cli.Store.ID != nil {
				banco.Anotar(ctx, "numero", cli.Store.ID.User)
			}
			anotarConexao(ctx, banco, true, "")
			fmt.Println("· conectado")

		case *events.Disconnected:
			// O whatsmeow nasce com EnableAutoReconnect e força a volta se os
			// pings falharem por três minutos. Isto NÃO pede ação de ninguém.
			anotarConexao(ctx, banco, false, "a conexão caiu — o cliente tenta voltar sozinho")
			fmt.Println("· desconectado · tentando voltar")

		case *events.KeepAliveTimeout:
			// Ainda conectado: o websocket não caiu, os pings é que pararam de
			// voltar. Só o motivo muda — desligar `conectado` aqui faria o
			// diagnóstico gritar por uma oscilação de trinta segundos.
			banco.Anotar(ctx, "motivo", fmt.Sprintf(
				"os pings pararam de voltar (%d seguidos; o último respondeu às %s). "+
					"A conexão se recupera sozinha, ou cai e volta",
				e.ErrorCount, e.LastSuccess.Format("15:04")))
			fmt.Printf("· ping sem resposta (%d)\n", e.ErrorCount)

		case *events.KeepAliveRestored:
			anotarConexao(ctx, banco, true, "")
			fmt.Println("· pings de volta")

		case *events.StreamReplaced:
			// O caso comum de verdade, e o mais fácil de causar sem perceber:
			// abrir o WhatsApp Web no navegador, ou um segundo `serve`.
			anotarConexao(ctx, banco, false,
				"outro dispositivo assumiu esta sessão — o WhatsApp Web abriu no navegador, "+
					"ou há um segundo `serve` no ar. Esta ponte NÃO volta sozinha: feche o outro "+
					"e rode `whatsapp-reader serve` de novo")
			fmt.Println("!! sessão assumida por outro dispositivo — esta ponte parou")
			fmt.Println("   feche o WhatsApp Web (ou o outro `serve`) e rode este de novo")

		case *events.TemporaryBan:
			// A issue #434 do whatsmeow é literalmente "Doesn't catch banned
			// event". O evento existe, traz código e prazo, e é a informação
			// mais cara de descobrir por qualquer outro caminho.
			anotarConexao(ctx, banco, false, "a conta foi restringida pelo WhatsApp · "+e.String())
			fmt.Println("!!", e.String())
			fmt.Println("   conta restrita não usa dispositivo conectado: a ponte fica fora até passar")

		case *events.ConnectFailure:
			anotarConexao(ctx, banco, false, fmt.Sprintf(
				"o WhatsApp recusou a conexão (%s) %s", e.Reason.String(), e.Message))
			fmt.Printf("!! conexão recusada: %s %s\n", e.Reason.String(), e.Message)

		case *events.StreamError:
			anotarConexao(ctx, banco, false, "erro de stream do WhatsApp, código "+e.Code)
			fmt.Println("!! erro de stream:", e.Code)

		case *events.ClientOutdated:
			// O erro que custou três tentativas na primeira instalação vinha
			// mudo, como `websocket: close 1006`. Aqui ele tem nome.
			anotarConexao(ctx, banco, false,
				"o WhatsApp recusou ESTA VERSÃO da ponte. O conserto é do mantenedor: "+
					"go get -u go.mau.fi/whatsmeow@latest, go mod tidy e recompilar")
			fmt.Println("!! cliente desatualizado — o WhatsApp recusou esta versão.")
			fmt.Println("   conserto: go get -u go.mau.fi/whatsmeow@latest e recompilar")

		case *events.LoggedOut:
			anotarConexao(ctx, banco, false,
				"a sessão foi encerrada no celular, ou o pareamento venceu. "+
					"Rode `whatsapp-reader serve` e escaneie o QR de novo")
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

	// A agenda do aparelho é a única fonte de nome: o history sync traz as
	// conversas sem ele. Falhar aqui não derruba a ponte — só deixa as
	// conversas com telefone no lugar do nome.
	if n, err := SincronizarNomes(ctx, cli, banco); err != nil {
		fmt.Fprintln(os.Stderr, "nomes da agenda:", err)
	} else if n > 0 {
		fmt.Printf("· %d conversas ganharam nome da sua agenda\n", n)
	}

	elo, err := AbrirElo(dir, cli, banco)
	if err != nil {
		return fmt.Errorf("elo: %w", err)
	}
	defer elo.Fechar()

	// A BATIDA. Enquanto ela é escrita, o daemon existe; quando para, ele
	// morreu — e é a única diferença observável entre "parado" e "morto" para
	// quem só tem o banco na mão, que é o caso do `mcp`.
	batendo, pararBatida := context.WithCancel(ctx)
	defer pararBatida()
	go baterSempre(batendo, banco)

	c, m := banco.Contagem(ctx)
	fmt.Printf("· ponte de pé · %d conversas, %d mensagens · %s\n", c, m, dir)
	fmt.Println("· deixe esta janela aberta. Ctrl+C encerra.")

	parar := make(chan os.Signal, 1)
	signal.Notify(parar, os.Interrupt, syscall.SIGTERM)
	<-parar
	pararBatida()
	// Primeiro a esteira: ela devolve à fila o que estava baixando enquanto a
	// conexão ainda está de pé.
	esteira.Fechar()
	// Saída limpa se declara: sem isto, um Ctrl+C ficaria noventa segundos
	// indistinguível de uma queda, e o diagnóstico diria "fora do ar" sobre
	// algo que a pessoa acabou de fechar de propósito.
	anotarConexao(context.Background(), banco, false, "encerrada aqui, com Ctrl+C")
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

/* Um lugar só para escrever as duas chaves, porque elas andam juntas: um
   `conectado` novo com o motivo velho deixa a ponte parecer caída pela razão
   errada, que é pior do que não dizer razão nenhuma. */

func anotarConexao(ctx context.Context, b *Banco, conectado bool, motivo string) {
	v := "0"
	if conectado {
		v = "1"
	}
	b.Anotar(ctx, "conectado", v)
	b.Anotar(ctx, "motivo", motivo)
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
