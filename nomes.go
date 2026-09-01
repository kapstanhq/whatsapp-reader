package main

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

/* O history sync traz as conversas quase todas SEM NOME — medido nesta máquina:
   205 conversas, 2 com nome. O nome não vem no sync; ele mora na agenda do
   aparelho, que o whatsmeow guarda em `whatsmeow_contacts` (1.080 contatos aqui).

   Isso não é detalhe cosmético. O contrato manda a prévia mostrar "o nome como
   ele conhece a pessoa" antes de qualquer envio, e uma prévia que diz
   "+55 51 99999-8888" para alguém que ele chama de Joana não cumpre o que
   promete: o corretor confere o número, não a pessoa. */

// A ordem é a de quem o corretor reconhece primeiro. O PushName vem por último
// porque é o que a PESSOA escolheu se chamar, e nem sempre é o nome dela — é o
// último recurso, não o primeiro.
func melhorNome(c types.ContactInfo) string {
	for _, n := range []string{c.FullName, c.BusinessName, c.FirstName, c.PushName} {
		if s := strings.TrimSpace(n); s != "" {
			return s
		}
	}
	return ""
}

// Roda uma vez, quando o daemon sobe e já está autenticado. Só PREENCHE o que
// está vazio: nome que já veio de uma mensagem ao vivo é mais recente que a
// agenda, e sobrescrever seria trocar o novo pelo velho.
func SincronizarNomes(ctx context.Context, cli *whatsmeow.Client, b *Banco) (int, error) {
	contatos, err := cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return 0, err
	}
	tocados := 0
	for jid, info := range contatos {
		nome := melhorNome(info)
		if nome == "" {
			continue
		}
		// ToNonAD porque a conversa é gravada sem o sufixo de dispositivo.
		n, err := b.NomearSeVazio(ctx, jid.ToNonAD().String(), nome)
		if err != nil {
			return tocados, err
		}
		tocados += n
	}
	return tocados, nil
}

// Devolve 1 se nomeou, 0 se a conversa não existe ou já tinha nome.
func (b *Banco) NomearSeVazio(ctx context.Context, jid, nome string) (int, error) {
	res, err := b.db.ExecContext(ctx, `
		UPDATE conversas SET nome = ?
		WHERE jid = ? AND COALESCE(nome, '') = ''`, nome, jid)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
