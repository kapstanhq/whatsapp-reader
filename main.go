package main

import (
	"fmt"
	"os"
	"path/filepath"
)

/* Um binário, dois modos. `serve` fica vivo e grava; `mcp` é curto e só lê.
   Dois processos porque a conexão precisa sobreviver ao Claude Code fechar —
   e um processo só, morrendo junto, perderia tudo que chegasse depois. */

func dirPadrao() string {
	casa, err := os.UserHomeDir()
	if err != nil {
		return ".whatsapp-reader"
	}
	return filepath.Join(casa, ".kapstan", "whatsapp-reader")
}

func main() {
	dir := os.Getenv("WHATSAPP_READER_DIR")
	if dir == "" {
		dir = dirPadrao()
	}

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "serve":
		if err := Servir(dir); err != nil {
			fmt.Fprintln(os.Stderr, "erro:", err)
			os.Exit(1)
		}
	case "mcp":
		if err := Mcp(dir); err != nil {
			fmt.Fprintln(os.Stderr, "erro:", err)
			os.Exit(1)
		}
	default:
		fmt.Println(`whatsapp-reader · as suas conversas, no seu computador

  serve    mantém a conexão viva e grava o que chega
  mcp      serve as tools para o agente (lê o banco)

  Ele LÊ sempre, e manda UMA por vez — com você vendo o texto e para quem
  vai, antes de sair. Disparo em massa não existe aqui: não há como pedir.

  os dados ficam em ` + dir + `
  outro lugar: variável WHATSAPP_READER_DIR`)
	}
}
