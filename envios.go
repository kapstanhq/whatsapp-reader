package main

import (
	"context"
	"database/sql"
	"time"
)

/* O REGISTRO do envio. Fica separado de `mensagens` de propósito: `mensagens` é
   o que o WhatsApp entregou, e esta tabela é o que ESTA ferramenta fez — com a
   prévia que foi mostrada, a hora em que foi mostrada, e o que aconteceu
   depois. A recusa também mora aqui: um envio barrado pela trava deixa linha,
   senão a trava é invisível para quem quiser auditá-la. */

const esquemaEnvios = `
CREATE TABLE IF NOT EXISTS envios (
  previa        TEXT PRIMARY KEY,
  conversa      TEXT NOT NULL,
  nome          TEXT,
  texto         TEXT NOT NULL,
  estado        TEXT NOT NULL,
  motivo        TEXT,
  preparado_em  INTEGER NOT NULL,
  enviado_em    INTEGER,
  msg_id        TEXT
);
CREATE INDEX IF NOT EXISTS idx_envios_em ON envios(preparado_em DESC);
CREATE INDEX IF NOT EXISTS idx_envios_enviado ON envios(enviado_em DESC);
`

func (b *Banco) GravarPrevia(ctx context.Context, id, conversa, nome, texto string, em time.Time) error {
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO envios (previa, conversa, nome, texto, estado, preparado_em)
		VALUES (?, ?, ?, ?, 'previsto', ?)`, id, conversa, nome, texto, em.Unix())
	return err
}

func (b *Banco) MarcarEnviado(ctx context.Context, id, msgID string, em time.Time) error {
	_, err := b.db.ExecContext(ctx, `
		UPDATE envios SET estado='enviado', enviado_em=?, msg_id=?
		 WHERE previa=? AND estado='previsto'`, em.Unix(), msgID, id)
	return err
}

func (b *Banco) MarcarRecusado(ctx context.Context, id, motivo string) error {
	_, err := b.db.ExecContext(ctx, `
		UPDATE envios SET estado='recusado', motivo=?
		 WHERE previa=? AND estado='previsto'`, motivo, id)
	return err
}

// A janela da trava. Conta do BANCO e não da memória: reiniciar o daemon não
// zera nada. `jaFalou` diz se ESTA conversa já recebeu algo na janela — se
// já, o teto de conversas distintas não se aplica a ela, porque responder de
// novo a quem se estava atendendo é conversa e não lote.
func (b *Banco) EnviosNaJanela(ctx context.Context, desde time.Time, jid string) (
	total, distintos int, jaFalou bool, ultimo time.Time, err error) {

	var seg sql.NullInt64
	err = b.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT conversa), MAX(enviado_em),
		       -- COALESCE porque MAX() sobre zero linhas devolve NULL, e o
		       -- driver recusa o Scan de NULL em bool: no primeiro envio da
		       -- vida do corretor a trava rebentava em vez de deixar passar.
		       COALESCE(MAX(CASE WHEN conversa = ? THEN 1 ELSE 0 END), 0)
		  FROM envios
		 WHERE estado='enviado' AND enviado_em >= ?`, jid, desde.Unix()).
		Scan(&total, &distintos, &seg, &jaFalou)
	if err != nil {
		return 0, 0, false, time.Time{}, err
	}
	if seg.Valid && seg.Int64 > 0 {
		ultimo = time.Unix(seg.Int64, 0)
	}
	return total, distintos, jaFalou, ultimo, nil
}

func (b *Banco) NomeDe(ctx context.Context, jid string) string {
	var nome string
	b.db.QueryRowContext(ctx,
		`SELECT COALESCE(nome,'') FROM conversas WHERE jid = ?`, jid).Scan(&nome)
	return nome
}

type EnvioVisto struct {
	Conversa, Nome, Texto, Estado, Motivo string
	Em                                    time.Time
}

// O que saiu POR AQUI. No celular do corretor, uma mensagem enviada pela ponte
// é igual a uma digitada por ele — esta é a única vista que as separa.
func (b *Banco) EnviosRecentes(ctx context.Context, desde time.Time, limite int) ([]EnvioVisto, error) {
	rows, err := b.db.QueryContext(ctx, `
		SELECT conversa, COALESCE(nome,''), texto, estado, COALESCE(motivo,''),
		       COALESCE(enviado_em, preparado_em)
		  FROM envios
		 WHERE COALESCE(enviado_em, preparado_em) >= ? AND estado <> 'previsto'
		 ORDER BY COALESCE(enviado_em, preparado_em) DESC LIMIT ?`, desde.Unix(), limite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnvioVisto
	for rows.Next() {
		var e EnvioVisto
		var seg int64
		if err := rows.Scan(&e.Conversa, &e.Nome, &e.Texto, &e.Estado, &e.Motivo, &seg); err != nil {
			return nil, err
		}
		e.Em = time.Unix(seg, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Quantas conversas distintas o corretor atendeu na hora mais cheia dos
// últimos 90 dias, PELO CELULAR. É deste número que sai o teto — não do gosto
// de quem escreveu a constante.
func (b *Banco) PicoDeConversasPorHora(ctx context.Context) int {
	var n int
	b.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(n), 0) FROM (
		  SELECT COUNT(DISTINCT conversa) AS n
		    FROM mensagens
		   WHERE de_mim = 1 AND em > CAST(strftime('%s','now','-90 days') AS INTEGER)
		   GROUP BY em / 3600)`).Scan(&n)
	return n
}
