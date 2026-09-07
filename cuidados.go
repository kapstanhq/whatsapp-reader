package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
)

/* Os três cuidados que o D197 decidiu e que não são trava de lote: a lista de
   quem pediu para não ser contatado, a prévia que envelhece quando o cliente
   escreve no meio-tempo, e o indicador de digitação.

   Os dois primeiros existem contra ERRO — ninguém escolheu mandar para quem
   pediu silêncio, nem mandar a resposta velha depois de o cliente falar de
   novo. O terceiro existe porque a Meta nomeia a AUSÊNCIA do indicador como
   sinal de robô, no white paper: "if an account continually sends messages
   without triggering the typing indicator … we will ban the account". */

// -- 1 · quem pediu para não ser contatado ---------------------------------

/* Um arquivo de texto no diretório da ponte, uma linha por pessoa. Fica FORA
   da carteira de propósito: a carteira mora onde o corretor escolheu (pode ser
   o Drive) e o daemon não a alcança. Quem escreve aqui é a skill, ou ele. */

const arquivoSilencio = "nao-contatar.txt"

// A comparação é pelo NÚMERO, não pelo jid inteiro: o mesmo contato aparece
// como 5551...@s.whatsapp.net e como @lid, e um bloqueio que só pega uma das
// formas não é bloqueio.
func soDigitos(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
		if r == '@' || r == ':' {
			break
		}
	}
	return b.String()
}

func (e *Elo) pediuSilencio(jid types.JID) (bool, string) {
	f, err := os.Open(filepath.Join(e.dir, arquivoSilencio))
	if err != nil {
		return false, "" // não existir é o caso comum, e não é erro
	}
	defer f.Close()

	alvo := soDigitos(jid.User)
	if alvo == "" {
		return false, ""
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		linha := strings.TrimSpace(sc.Text())
		if linha == "" || strings.HasPrefix(linha, "#") {
			continue
		}
		// "5551999998888 · Joana, pediu em 12/08" — o motivo vive na mesma
		// linha, depois de qualquer separador, e volta na recusa.
		campo := linha
		motivo := ""
		if i := strings.IndexAny(linha, "·|#,;\t"); i >= 0 {
			campo, motivo = linha[:i], strings.TrimSpace(strings.Trim(linha[i:], "·|#,;\t "))
		}
		if n := soDigitos(campo); n != "" && (n == alvo || strings.HasSuffix(alvo, n) || strings.HasSuffix(n, alvo)) {
			return true, motivo
		}
	}
	return false, ""
}

/* E o comando que escreve nela, porque o contrato do pack chama os dois lugares
   de obrigatórios — a carteira e esta lista — e a skill não tinha como cumprir o
   segundo: o arquivo mora no diretório da PONTE, que a carteira não conhece (ela
   pode estar no Google Drive, onde o daemon não chega). Sem isto, um pedido de
   silêncio ficava metade escrito. */

func NaoContatar(dir string, args []string) error {
	caminho := filepath.Join(dir, arquivoSilencio)

	if len(args) == 0 {
		b, err := os.ReadFile(caminho)
		if err != nil {
			fmt.Println("ninguém na lista de não contatar.")
			fmt.Println("para pôr:  whatsapp-reader nao-contatar 5551999998888 \"pediu em 12/08\"")
			return nil
		}
		fmt.Print(string(b))
		return nil
	}

	tirar := args[0] == "--tirar"
	if tirar {
		if len(args) < 2 {
			return fmt.Errorf("falta o número: whatsapp-reader nao-contatar --tirar 5551999998888")
		}
		args = args[1:]
	}
	numero := soDigitos(args[0])
	if numero == "" {
		return fmt.Errorf("número inválido: %q", args[0])
	}
	motivo := strings.TrimSpace(strings.Join(args[1:], " "))

	var linhas []string
	if b, err := os.ReadFile(caminho); err == nil {
		linhas = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	} else {
		// O cabeçalho nasce junto: quem abrir o arquivo daqui a um mês precisa
		// saber o que ele faz sem procurar o README.
		linhas = []string{
			"# Quem pediu para não ser contatado. Uma linha por pessoa:",
			"#   <número> · <motivo e data>",
			"# A ponte RECUSA qualquer envio para quem está aqui, e a recusa diz o motivo.",
			"# Tirar alguém:  whatsapp-reader nao-contatar --tirar <número>",
		}
	}

	sobrou := linhas[:0]
	achou := false
	for _, l := range linhas {
		corte := strings.TrimSpace(l)
		if corte != "" && !strings.HasPrefix(corte, "#") {
			campo := corte
			if i := strings.IndexAny(corte, "·|#,;\t"); i >= 0 {
				campo = corte[:i]
			}
			if n := soDigitos(campo); n != "" && (n == numero ||
				strings.HasSuffix(numero, n) || strings.HasSuffix(n, numero)) {
				achou = true
				continue // a linha velha sai: ou é remoção, ou é motivo novo
			}
		}
		sobrou = append(sobrou, l)
	}

	if tirar {
		if !achou {
			fmt.Printf("%s não estava na lista.\n", numero)
			return nil
		}
		if err := gravarLinhas(caminho, sobrou); err != nil {
			return err
		}
		fmt.Printf("%s saiu da lista — a ponte volta a poder mandar para esse número.\n", numero)
		return nil
	}

	linha := numero
	if motivo != "" {
		linha += " · " + motivo
	}
	sobrou = append(sobrou, linha)
	if err := gravarLinhas(caminho, sobrou); err != nil {
		return err
	}
	verbo := "entrou na lista"
	if achou {
		verbo = "já estava na lista, e o motivo foi trocado"
	}
	fmt.Printf("%s %s. Nenhuma skill manda mensagem para ele a partir de agora.\n", numero, verbo)
	fmt.Println("Falta o outro lado: `não contatar: sim` no arquivo dele, na carteira.")
	return nil
}

func gravarLinhas(caminho string, linhas []string) error {
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return err
	}
	return os.WriteFile(caminho, []byte(strings.Join(linhas, "\n")+"\n"), 0o600)
}

// -- 2 · a prévia envelhece quando o cliente escreve -----------------------

/* O corretor responde pelo celular enquanto a prévia espera na tela, e o
   agente manda a resposta velha logo atrás — ou pior, a pergunta que ele
   acabou de responder. Mensagem DELE (de_mim=0) depois da prévia mata a
   prévia. Mensagem do próprio corretor não mata: ele pode ter mandado o
   "oi" antes e a prévia é a continuação. */

func (b *Banco) RecebidaDesde(ctx context.Context, conversa string, desde time.Time) (bool, time.Time, error) {
	var em int64
	err := b.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(em), 0) FROM mensagens
		WHERE conversa = ? AND de_mim = 0 AND em > ?`,
		conversa, desde.Unix()).Scan(&em)
	if err != nil {
		return false, time.Time{}, err
	}
	if em == 0 {
		return false, time.Time{}, nil
	}
	return true, time.Unix(em, 0), nil
}

// -- 3 · o indicador de digitação ------------------------------------------

/* Antes de cada envio, e é barato. O tempo sai do tamanho do texto — 45
   palavras por minuto é a média que as bibliotecas usam —, com piso de 1,5 s
   e teto de 8: acima disso a espera passa a custar mais do que protege.

   Falha aqui não impede o envio. A presença é um sinal a mais, e perder o
   sinal é pior que perder a mensagem. */

func digitando(ctx context.Context, cli interface {
	SendPresence(context.Context, types.Presence) error
	SendChatPresence(context.Context, types.JID, types.ChatPresence, types.ChatPresenceMedia) error
}, jid types.JID, texto string) {
	// Sem "available" o servidor descarta a presença de conversa.
	if err := cli.SendPresence(ctx, types.PresenceAvailable); err != nil {
		return
	}
	if err := cli.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText); err != nil {
		return
	}
	espera := time.Duration(float64(len(strings.Fields(texto))) / 45.0 * float64(time.Minute))
	if espera < 1500*time.Millisecond {
		espera = 1500 * time.Millisecond
	} else if espera > 8*time.Second {
		espera = 8 * time.Second
	}
	select {
	case <-time.After(espera):
	case <-ctx.Done():
	}
	cli.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
}

// -- o texto da recusa ------------------------------------------------------

func erroSilencio(nome, motivo string) error {
	quem := nome
	if quem == "" {
		quem = "esta pessoa"
	}
	if motivo != "" {
		return fmt.Errorf("%s está na lista de não contatar (%s). "+
			"Para tirar, edite %s no diretório da ponte", quem, motivo, arquivoSilencio)
	}
	return fmt.Errorf("%s está na lista de não contatar. "+
		"Para tirar, edite %s no diretório da ponte", quem, arquivoSilencio)
}

// -- 4 · como chamar quem vai receber --------------------------------------

/* O contrato manda a prévia mostrar "o nome como ele conhece a pessoa". Quando
   o contato não está salvo — e é assim que TODO lead novo chega — não há nome,
   e mostrar o jid duas vezes não diz nada a quem autoriza.

   Então o telefone vira legível e o fato de não estar salvo é dito na cara: é
   informação que muda a decisão do corretor, não ruído. */

func comoChamar(nome, jid string) string {
	if strings.TrimSpace(nome) != "" {
		return nome
	}
	n := soDigitos(jid)
	if n == "" {
		return "contato não salvo"
	}
	return telefoneBR(n) + " · não está salvo"
}

// 555192601031 -> +55 51 9260-1031. Fora do formato brasileiro, devolve o que
// veio: um número estrangeiro meio formatado engana mais que um número cru.
func telefoneBR(n string) string {
	if !strings.HasPrefix(n, "55") || len(n) < 12 || len(n) > 13 {
		return "+" + n
	}
	ddd, resto := n[2:4], n[4:]
	corte := len(resto) - 4
	return fmt.Sprintf("+55 %s %s-%s", ddd, resto[:corte], resto[corte:])
}

// -- 5 · quem nunca escreveu -----------------------------------------------

/* A fronteira de risco do D197, e ela não é gradual: responder quem te
   escreveu é o caso mais seguro que existe; abrir conversa com quem nunca
   respondeu é o exemplo de zona cinzenta do próprio white paper da Meta, e a
   denúncia ali é a de peso máximo.

   A ponte NÃO impede — é decisão do corretor, e ele pode ter o número porque a
   pessoa deu na feira de imóveis. Ela informa, uma vez, na prévia. */

func (b *Banco) JaEscreveu(ctx context.Context, conversa string) (bool, time.Time) {
	var em int64
	b.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(em), 0) FROM mensagens
		WHERE conversa = ? AND de_mim = 0`, conversa).Scan(&em)
	if em == 0 {
		return false, time.Time{}
	}
	return true, time.Unix(em, 0)
}

/* Existe alguma mensagem nesta conversa, em qualquer direção? É outra pergunta
   que `JaEscreveu`, e a diferença decide se o envio vai sequer sair.

   Desde 02/07/2026 o WhatsApp RECUSA `SendMessage` com erro 463 para quem nunca
   trocou mensagem com a conta (whatsmeow #1197, aberta). Salvar o contato na
   agenda não resolve — só uma mensagem saindo do aplicativo oficial abre a
   conversa. Depois disso a ponte responde normalmente. */

func (b *Banco) ConversaVirgem(ctx context.Context, conversa string) bool {
	var n int
	b.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mensagens WHERE conversa = ?`, conversa).Scan(&n)
	return n == 0
}

func avisoDeRisco(jaEscreveu, virgem bool, quando time.Time) string {
	if virgem {
		// Não é tutela nossa: é a plataforma recusando. Dizer isto ANTES vale
		// mais do que traduzir o 463 depois — o corretor decide com o caminho
		// na mão, em vez de descobrir com a mensagem pronta e um erro na tela.
		return "ATENÇÃO · não existe conversa com esta pessoa neste WhatsApp: nem ela " +
			"escreveu, nem você. Desde julho de 2026 o WhatsApp RECUSA esse envio (erro 463), " +
			"e salvar na agenda não resolve — quem abre a conversa tem que ser o aplicativo do " +
			"celular. O caminho é o corretor mandar a primeira mensagem por lá; depois disso a " +
			"ponte responde normalmente. Ofereça o texto para ele copiar."
	}
	if !jaEscreveu {
		return "ATENÇÃO · esta pessoa NUNCA escreveu para você por aqui — você já mandou, " +
			"ela não respondeu. Abrir conversa com quem nunca respondeu é o que faz uma conta " +
			"ser restringida, e a restrição desliga esta ponte. Diga isso ao corretor antes de " +
			"ele autorizar."
	}
	dias := int(time.Since(quando).Hours() / 24)
	switch {
	case dias == 0:
		return "ela escreveu hoje · é o caso mais seguro que existe"
	case dias == 1:
		return "ela escreveu ontem"
	case dias < 30:
		return fmt.Sprintf("ela escreveu há %d dias", dias)
	}
	return fmt.Sprintf("ela escreveu pela última vez há %d dias, em %s — faz tempo, "+
		"e a mensagem vai chegar como quem reabre assunto", dias, quando.Format("02/01/2006"))
}

// -- 6 · a mesma mensagem duas vezes ---------------------------------------

/* Aconteceu na primeira semana de uso, e não por burla: um script rodou duas
   vezes e a mesma mensagem saiu com 26 segundos de intervalo. As travas de
   lote não pegam — é UMA conversa, e responder de novo a quem já se atendeu
   na hora é conversa, não lote.

   A divisão é a do D197: erro se recusa, escolha se avisa.

   Ninguém ESCOLHE mandar o mesmo texto para a mesma pessoa em menos de um
   minuto — isso é dedo duplo, ou um retry de quem achou que a primeira
   falhou. Passou de um minuto, vira decisão legítima ("ela não viu, manda de
   novo"), e aí a prévia diz que já saiu e quem decide é o corretor. */

const janelaDedoDuplo = time.Minute

// Devolve quando o mesmo texto saiu para a mesma conversa, dentro da janela.
func (b *Banco) MesmoTextoSaiu(ctx context.Context, conversa, texto string, desde time.Time) (bool, time.Time) {
	var em int64
	b.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(enviado_em), 0) FROM envios
		WHERE conversa = ? AND texto = ? AND estado = 'enviado' AND enviado_em > ?`,
		conversa, texto, desde.Unix()).Scan(&em)
	if em == 0 {
		return false, time.Time{}
	}
	return true, time.Unix(em, 0)
}
