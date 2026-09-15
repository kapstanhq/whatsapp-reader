package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

/* O que as migrações precisam garantir é o caso de uso real: banco novo, banco
   de uma versão anterior com dados, os dois processos abrindo juntos, e um
   binário velho diante de um banco que um novo já migrou. */

func versaoDe(t *testing.T, caminho string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+caminho)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func tabelaExiste(t *testing.T, b *Banco, nome string) bool {
	t.Helper()
	var n int
	b.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, nome).Scan(&n)
	return n == 1
}

func ultimaVersao() int { return migracoes[len(migracoes)-1].versao }

func TestMigrarBancoNovo(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "m.db")
	b, err := AbrirBanco(caminho)
	if err != nil {
		t.Fatal(err)
	}
	for _, tabela := range []string{"midias", "transcricoes", "mensagens", "estado"} {
		if !tabelaExiste(t, b, tabela) {
			t.Errorf("tabela %s não existe depois de abrir", tabela)
		}
	}
	b.Fechar()
	if v := versaoDe(t, caminho); v != ultimaVersao() {
		t.Errorf("user_version = %d, esperava %d", v, ultimaVersao())
	}
}

func TestMigrarBancoDaVersaoAnteriorGuardaOsDados(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "m.db")
	// O banco como a versão anterior o deixava: só os CREATE IF NOT EXISTS,
	// user_version 0, e uma mensagem dentro.
	velho, err := sql.Open("sqlite", "file:"+caminho+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{esquema, esquemaEnvios, esquemaEstado} {
		if _, err := velho.Exec(e); err != nil {
			t.Fatal(err)
		}
	}
	em := time.Date(2026, 9, 9, 17, 4, 0, 0, time.Local)
	if _, err := velho.Exec(`INSERT INTO mensagens (id, conversa, de_mim, texto, em) VALUES ('X1','5551@s.whatsapp.net',0,'seguro vence por estes dias',?)`, em.Unix()); err != nil {
		t.Fatal(err)
	}
	velho.Close()

	b, err := AbrirBanco(caminho)
	if err != nil {
		t.Fatalf("abrir banco da versão anterior: %v", err)
	}
	defer b.Fechar()
	if !tabelaExiste(t, b, "midias") {
		t.Error("a migração não criou midias no banco antigo")
	}
	ms, err := b.ListarMensagens(context.Background(), "", "seguro", time.Time{}, time.Time{}, 10)
	if err != nil || len(ms) != 1 {
		t.Errorf("a mensagem de antes da migração sumiu: %v %v", ms, err)
	}
}

func TestMigrarDuasVezesNaoFazNada(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "m.db")
	for i := 0; i < 2; i++ {
		b, err := AbrirBanco(caminho)
		if err != nil {
			t.Fatalf("abertura %d: %v", i+1, err)
		}
		b.Fechar()
	}
	if v := versaoDe(t, caminho); v != ultimaVersao() {
		t.Errorf("user_version = %d, esperava %d", v, ultimaVersao())
	}
}

func TestMigrarComServeEMcpAbrindoJuntos(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "m.db")
	var wg sync.WaitGroup
	erros := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := AbrirBanco(caminho)
			if err != nil {
				erros <- err
				return
			}
			b.Fechar()
		}()
	}
	wg.Wait()
	close(erros)
	for err := range erros {
		t.Errorf("abertura concorrente falhou: %v", err)
	}
	if v := versaoDe(t, caminho); v != ultimaVersao() {
		t.Errorf("user_version = %d, esperava %d", v, ultimaVersao())
	}
}

func TestBinarioVelhoToleraBancoMaisNovo(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "m.db")
	b, err := AbrirBanco(caminho)
	if err != nil {
		t.Fatal(err)
	}
	// Um binário do futuro passou por aqui.
	if _, err := b.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	b.Fechar()

	b, err = AbrirBanco(caminho)
	if err != nil {
		t.Fatalf("banco de versão mais nova tem que abrir: %v", err)
	}
	b.Fechar()
	if v := versaoDe(t, caminho); v != 99 {
		t.Errorf("a versão do binário mais novo foi rebaixada para %d", v)
	}
}
