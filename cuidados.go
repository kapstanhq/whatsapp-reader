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

func avisoDeRisco(jaEscreveu bool, quando time.Time) string {
	if !jaEscreveu {
		return "ATENÇÃO · esta pessoa NUNCA escreveu para você por aqui. Abrir conversa " +
			"com quem nunca respondeu é o que faz uma conta ser restringida, e a restrição " +
			"desliga esta ponte. Diga isso ao corretor antes de ele autorizar."
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
