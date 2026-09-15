package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

/* A SESSÃO NO DISCO, e por que ela precisa esperar a vez.

   Em 15/09/2026 a ponte voltou depois de cinco dias fora e o WhatsApp despejou
   o atraso de uma vez: ~70 mensagens não foram decifradas, todas com
   "database is locked (SQLITE_BUSY)". Não era outro processo — só o daemon abre
   o `sessao.db`. Era o próprio whatsmeow, com várias goroutines gravando
   identidade e sender key ao mesmo tempo num arquivo sem WAL e sem
   busy_timeout: a segunda escrita não esperava, falhava na hora, e mensagem que
   não decifra não volta.

   Os pragmas vão no DSN, e não num Exec depois de abrir, porque o database/sql
   abre várias conexões e cada uma nasce sem eles. `foreign_keys` é exigência
   do whatsmeow. `synchronous` fica no padrão (FULL): perder o último commit do
   ratchet do Signal num corte de luz custa mais que a velocidade ganha. */

func dsnSessao(dir string) string {
	return "file:" + filepath.Join(dir, "sessao.db") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
}

// A troca para WAL precisa do arquivo sozinho. Se ela esbarra, quase sempre é
// um segundo `serve` segurando a sessão — e é isso que quem lê precisa saber,
// não o código do SQLite.
func erroSessao(err error) error {
	if msg := err.Error(); strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked") {
		return fmt.Errorf("sessao.db está aberta por outro processo — há um segundo `serve` no ar? "+
			"Feche o outro e rode este de novo (%w)", err)
	}
	return fmt.Errorf("abrir sessão: %w", err)
}
