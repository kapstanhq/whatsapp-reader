package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

/* O defeito que isto guarda foi medido, não imaginado: sem busy_timeout nem WAL
   no sessao.db, a rajada de mensagens atrasadas travou o próprio daemon e ~70
   mensagens ficaram sem decifrar. O teste confere que TODA conexão do pool
   nasce com os três pragmas — é por conexão que eles valem. */

func TestDsnSessaoLigaWALEEspera(t *testing.T) {
	db, err := sql.Open("sqlite", dsnSessao(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var modo string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&modo); err != nil {
		t.Fatal(err)
	}
	if modo != "wal" {
		t.Errorf("journal_mode = %q, esperava wal", modo)
	}
	var espera int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&espera); err != nil {
		t.Fatal(err)
	}
	if espera != 10000 {
		t.Errorf("busy_timeout = %d, esperava 10000", espera)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, esperava 1 — sem isto o whatsmeow recusa a sessão", fk)
	}
}

func TestDsnSessaoAbreComOWhatsmeow(t *testing.T) {
	ctx := context.Background()
	c, err := sqlstore.New(ctx, "sqlite", dsnSessao(t.TempDir()), waLog.Noop)
	if err != nil {
		t.Fatalf("o whatsmeow recusou o DSN: %v", err)
	}
	defer c.Close()
	if _, err := c.GetFirstDevice(ctx); err != nil {
		t.Fatalf("device: %v", err)
	}
}

func TestErroSessaoApontaOSegundoServe(t *testing.T) {
	bruto := errors.New("failed to upgrade database: database is locked (5) (SQLITE_BUSY)")
	err := erroSessao(bruto)
	if !strings.Contains(err.Error(), "segundo `serve`") {
		t.Errorf("a trava devia apontar o segundo serve, disse: %v", err)
	}
	if !errors.Is(err, bruto) {
		t.Error("o erro original tem que continuar alcançável")
	}
	if outro := erroSessao(errors.New("disk I/O error")); strings.Contains(outro.Error(), "segundo") {
		t.Errorf("erro que não é trava não pode culpar um segundo serve: %v", outro)
	}
}
