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
