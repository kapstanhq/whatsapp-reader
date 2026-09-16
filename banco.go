package main

import (
	"context"
	"database/sql"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

/* O banco é a razão de a ponte existir: o WhatsApp entrega o histórico UMA vez,
   no pareamento, e depois só manda o que chega. Perdeu, não vem de novo — por
   isso a gravação é a primeira coisa a funcionar, e a última a poder falhar
   em silêncio.

   TEMPO É INTEIRO, segundos desde a época, e não TIMESTAMP. A primeira versão
   usava TIMESTAMP e três consultas voltaram vazias: `COALESCE(em, 0)` faz a
   coluna perder o tipo declarado e o driver recusa o Scan em time.Time, e o
   formato de texto de quem grava não é obrigatoriamente o de quem lê. Inteiro
   compara igual em qualquer caminho, e a conversão mora nas bordas. */

type Banco struct{ db *sql.DB }

const esquema = `
CREATE TABLE IF NOT EXISTS conversas (
  jid        TEXT PRIMARY KEY,
  nome       TEXT,
  ultima_em  INTEGER
);
CREATE TABLE IF NOT EXISTS mensagens (
  id         TEXT NOT NULL,
  conversa   TEXT NOT NULL,
  remetente  TEXT,
  de_mim     INTEGER NOT NULL DEFAULT 0,
  texto      TEXT,
  midia      TEXT,
  em         INTEGER NOT NULL,
  PRIMARY KEY (id, conversa)
);
CREATE INDEX IF NOT EXISTS idx_msg_conversa_em ON mensagens(conversa, em DESC);
CREATE INDEX IF NOT EXISTS idx_msg_em          ON mensagens(em DESC);
`

func AbrirBanco(caminho string) (*Banco, error) {
	db, err := sql.Open("sqlite", "file:"+caminho+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// O daemon e o `ponte mcp` abrem o MESMO arquivo ao mesmo tempo. Sem WAL,
	// a leitura do MCP trava a escrita do daemon e mensagem se perde.
	if err := ligarWAL(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(esquema); err != nil {
		return nil, err
	}
	if _, err := db.Exec(esquemaEnvios); err != nil {
		return nil, err
	}
	if _, err := db.Exec(esquemaEstado); err != nil {
		return nil, err
	}
	// Depois do esquema de sempre, o que só migração consegue trazer. Ver esquema.go.
	if _, err := migrar(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	return &Banco{db: db}, nil
}

func (b *Banco) Fechar() error { return b.db.Close() }

func (b *Banco) GravarConversa(ctx context.Context, jid, nome string, em time.Time) error {
	// O nome só sobrescreve quando vem preenchido: o history sync às vezes
	// entrega a conversa sem nome, e um UPDATE cego apagaria o que já se sabia.
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO conversas (jid, nome, ultima_em) VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
		  nome      = COALESCE(NULLIF(excluded.nome, ''), conversas.nome),
		  ultima_em = MAX(COALESCE(conversas.ultima_em, 0), excluded.ultima_em)`,
		jid, nome, em.Unix())
	return err
}

type Mensagem struct {
	ID, Conversa, Remetente, Texto, Midia string
	DeMim                                 bool
	Em                                    time.Time
}

func (b *Banco) GravarMensagem(ctx context.Context, m Mensagem) error {
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO mensagens (id, conversa, remetente, de_mim, texto, midia, em)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, conversa) DO UPDATE SET
		  texto = COALESCE(NULLIF(excluded.texto, ''), mensagens.texto)`,
		m.ID, m.Conversa, m.Remetente, m.DeMim, m.Texto, m.Midia, m.Em.Unix())
	return err
}

func (b *Banco) Contagem(ctx context.Context) (conversas, mensagens int) {
	b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversas`).Scan(&conversas)
	b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mensagens`).Scan(&mensagens)
	return
}

/* ── as consultas que o modo mcp expõe ──────────────────────────────────
   Tudo aqui é leitura. O daemon é o único que escreve, e é por isso que os
   dois podem abrir o mesmo arquivo sem disputa. */

type ConversaVista struct {
	JID, Nome string
	Ultima    time.Time
	Total     int
}

func (b *Banco) ListarConversas(ctx context.Context, busca string, limite int) ([]ConversaVista, error) {
	rows, err := b.db.QueryContext(ctx, `
		SELECT c.jid, COALESCE(c.nome,''), COALESCE(c.ultima_em, 0),
		       (SELECT COUNT(*) FROM mensagens m WHERE m.conversa = c.jid)
		  FROM conversas c
		 WHERE (? = '' OR c.nome LIKE '%'||?||'%' OR c.jid LIKE '%'||?||'%')
		 ORDER BY c.ultima_em DESC LIMIT ?`, busca, busca, busca, limite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConversaVista
	for rows.Next() {
		var c ConversaVista
		var em int64
		if err := rows.Scan(&c.JID, &c.Nome, &em, &c.Total); err != nil {
			return nil, err
		}
		if em > 0 {
			c.Ultima = time.Unix(em, 0)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type MensagemVista struct {
	Conversa, Nome, Remetente, Texto, Midia string
	DeMim                                   bool
	Em                                      time.Time
	Audio                                   AudioVisto
}

// O que a esteira sabe de um áudio. Estado vazio: a mensagem não tem linha em
// midias — é de antes da migração, ou foi gravada por um daemon antigo.
type AudioVisto struct {
	Estado, Motivo           string // midias.estado e midias.erro
	Segundos                 int
	Transcricao, Texto, Erro string // transcricoes.estado, .texto e .erro
}

// Toda leitura de mensagem traz o áudio ao lado: sem a linha de midias e a de
// transcricoes, uma nota de voz é só "[áudio]", e quem lê não sabe se ela ainda
// vai virar texto ou se nunca vai.
const selectMensagem = `
	SELECT m.conversa, COALESCE(c.nome,''), COALESCE(m.remetente,''),
	       COALESCE(m.texto,''), COALESCE(m.midia,''), m.de_mim, m.em,
	       COALESCE(md.estado,''), COALESCE(md.erro,''), COALESCE(md.segundos,0),
	       COALESCE(t.estado,''), COALESCE(t.texto,''), COALESCE(t.erro,'')
	  FROM mensagens m
	  LEFT JOIN conversas c    ON c.jid = m.conversa
	  LEFT JOIN midias md      ON md.mensagem = m.id AND md.conversa = m.conversa AND md.tipo = 'audio'
	  LEFT JOIN transcricoes t ON t.mensagem = m.id AND t.conversa = m.conversa`

func lerMensagens(rows *sql.Rows) ([]MensagemVista, error) {
	defer rows.Close()
	var out []MensagemVista
	for rows.Next() {
		var m MensagemVista
		var em int64
		if err := rows.Scan(&m.Conversa, &m.Nome, &m.Remetente, &m.Texto, &m.Midia, &m.DeMim, &em,
			&m.Audio.Estado, &m.Audio.Motivo, &m.Audio.Segundos,
			&m.Audio.Transcricao, &m.Audio.Texto, &m.Audio.Erro); err != nil {
			return nil, err
		}
		m.Em = time.Unix(em, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// A condição se monta com o que veio. A versão anterior tinha os quatro filtros
// fixos no SQL, desligados por `? = 0` — e como time.Time{}.Unix() é
// -62135596800, e não zero, o "desligado" nunca valia: toda consulta pedia
// mensagens posteriores ao ano 1, e voltava vazia.
func (b *Banco) ListarMensagens(ctx context.Context, conversa, busca string,
	depois, antes time.Time, limite int) ([]MensagemVista, error) {

	cond := []string{"1=1"}
	args := []any{}
	if conversa != "" {
		cond = append(cond, "m.conversa = ?")
		args = append(args, conversa)
	}
	if busca != "" {
		// A transcrição entra na busca: "calhas" dito num áudio é achado como se
		// tivesse sido escrito.
		cond = append(cond, "(m.texto LIKE ? OR t.texto LIKE ?)")
		args = append(args, "%"+busca+"%", "%"+busca+"%")
	}
	if !depois.IsZero() {
		cond = append(cond, "m.em >= ?")
		args = append(args, depois.Unix())
	}
	if !antes.IsZero() {
		cond = append(cond, "m.em <= ?")
		args = append(args, antes.Unix())
	}
	args = append(args, limite)

	rows, err := b.db.QueryContext(ctx, selectMensagem+`
		 WHERE `+strings.Join(cond, " AND ")+`
		 ORDER BY m.em DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	return lerMensagens(rows)
}

// Quantos dias de silêncio, e de quem foi a última palavra. É o que a skill
// de retomada precisa saber antes de escrever qualquer coisa — e, quando a
// última é uma nota de voz, o que foi dito nela. ok=false: conversa sem mensagem.
func (b *Banco) UltimaInteracao(ctx context.Context, conversa string) (m MensagemVista, ok bool, err error) {
	rows, err := b.db.QueryContext(ctx, selectMensagem+`
		 WHERE m.conversa = ? ORDER BY m.em DESC LIMIT 1`, conversa)
	if err != nil {
		return MensagemVista{}, false, err
	}
	ms, err := lerMensagens(rows)
	if err != nil || len(ms) == 0 {
		return MensagemVista{}, false, err
	}
	return ms[0], true, nil
}
