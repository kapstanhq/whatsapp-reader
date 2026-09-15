package main

import (
	"context"
	"database/sql"
	"fmt"
)

/* AS MIGRAÇÕES, e a regra que deixa dois binários de versões diferentes
   dividirem o mesmo arquivo.

   Até aqui o esquema era só `CREATE ... IF NOT EXISTS`, rodado por quem abrisse
   o banco primeiro. Serve para tabela nova e para nada mais: uma coluna nova
   nunca chegaria a um banco que já existe. E o arquivo é aberto por DOIS
   processos — o `serve` e o `mcp` —, que podem ser de versões diferentes: o
   corretor atualiza o binário e o daemon velho continua na janela.

   Por isso as migrações são SÓ ADITIVAS: tabela nova, ou coluna nova com
   default. Nunca renomear, nunca remover, nunca uma coluna NOT NULL sem default
   numa tabela que o binário velho ainda grava. Assim o velho segue lendo e
   gravando o que conhece, e o novo acrescenta o resto.

   O número mora no `PRAGMA user_version`: é atômico com o DDL, não cria tabela,
   e aparece em qualquer ferramenta de SQLite. A leitura e a aplicação acontecem
   dentro de um BEGIN IMMEDIATE — o `serve` e o `mcp` subindo juntos se
   enfileiram pelo busy_timeout, e o segundo relê o número e não faz nada.

   Versão maior que a conhecida NÃO é erro: é um binário velho num banco que um
   novo já migrou, exatamente o caso que a regra aditiva existe para permitir. */

type migracao struct {
	versao int
	nome   string
	sql    string
}

// Uma vez publicada, uma migração não muda: quem já rodou não roda de novo.
// Correção vira migração nova, com o número seguinte.
var migracoes = []migracao{
	{1, "mídia e transcrição", `
CREATE TABLE IF NOT EXISTS midias (
  mensagem    TEXT NOT NULL,
  conversa    TEXT NOT NULL,
  tipo        TEXT NOT NULL,
  mime        TEXT,
  segundos    INTEGER,
  voz         INTEGER NOT NULL DEFAULT 0,
  tamanho     INTEGER,
  sha256      TEXT,
  anexo       BLOB,
  em          INTEGER NOT NULL,
  origem      INTEGER NOT NULL DEFAULT 1,
  estado      TEXT NOT NULL,
  tentativas  INTEGER NOT NULL DEFAULT 0,
  proxima_em  INTEGER NOT NULL DEFAULT 0,
  pedida_em   INTEGER,
  arquivo     TEXT,
  erro        TEXT,
  baixada_em  INTEGER,
  apagada_em  INTEGER,
  PRIMARY KEY (mensagem, conversa)
);
CREATE INDEX IF NOT EXISTS idx_midias_fila ON midias(origem, em DESC) WHERE estado = 'pendente';
CREATE INDEX IF NOT EXISTS idx_midias_sha  ON midias(sha256);

CREATE TABLE IF NOT EXISTS transcricoes (
  mensagem    TEXT NOT NULL,
  conversa    TEXT NOT NULL,
  estado      TEXT NOT NULL DEFAULT 'pendente',
  texto       TEXT,
  idioma      TEXT,
  motor       TEXT,
  modelo      TEXT,
  tentativas  INTEGER NOT NULL DEFAULT 0,
  proxima_em  INTEGER NOT NULL DEFAULT 0,
  erro        TEXT,
  levou_ms    INTEGER,
  feita_em    INTEGER,
  PRIMARY KEY (mensagem, conversa)
);
CREATE INDEX IF NOT EXISTS idx_transcricoes_fila ON transcricoes(proxima_em) WHERE estado = 'pendente';
`},
	// O texto como o motor entregou, antes das correções do vocabulário, e a
	// dica que foi junto. Com o bruto, um erro novo no vocabulário corrige as
	// transcrições antigas sem transcrever de novo; com a dica, dá para explicar
	// por que o motor escreveu o que escreveu.
	{2, "texto bruto e dica da transcrição", `
ALTER TABLE transcricoes ADD COLUMN bruto TEXT;
ALTER TABLE transcricoes ADD COLUMN dica TEXT;
`},
}

// Devolve a versão em que o banco ficou.
func migrar(ctx context.Context, db *sql.DB) (versao int, err error) {
	// Conexão dedicada: o BEGIN e o COMMIT precisam cair na MESMA conexão, e o
	// pool do database/sql não garante isso entre dois Exec soltos.
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("migrar: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, fmt.Errorf("migrar: não consegui a vez no banco: %w", err)
	}
	concluiu := false
	defer func() {
		if !concluiu {
			conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&versao); err != nil {
		return 0, fmt.Errorf("migrar: ler a versão: %w", err)
	}
	for _, m := range migracoes {
		if m.versao <= versao {
			continue
		}
		if _, err := conn.ExecContext(ctx, m.sql); err != nil {
			return versao, fmt.Errorf("migração %d (%s): %w", m.versao, m.nome, err)
		}
		// PRAGMA não aceita parâmetro; o número vem da lista acima, nunca de fora.
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.versao)); err != nil {
			return versao, fmt.Errorf("migração %d (%s): gravar a versão: %w", m.versao, m.nome, err)
		}
		versao = m.versao
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return versao, fmt.Errorf("migrar: %w", err)
	}
	concluiu = true
	return versao, nil
}
