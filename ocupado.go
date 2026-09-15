package main

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// O modernc devolve a trava do SQLite como texto; é por ele que se reconhece.
func bancoOcupado(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

/* A troca para WAL é o único passo da abertura que NÃO espera o busy_timeout:
   com outra conexão no arquivo, o SQLite devolve SQLITE_BUSY na hora. Medido em
   15/09/2026 com quatro aberturas simultâneas de um banco novo: as 21 falhas
   em 15 rodadas foram todas aqui, nenhuma no esquema nem na migração. É o caso
   do `serve` e do `mcp` subindo juntos na primeira vez.

   Então ela pergunta antes — num banco que já é WAL a troca nem acontece — e,
   se esbarrar, tenta de novo com espera crescente e um pouco de sorteio, para
   os dois processos não voltarem no mesmo compasso. Desiste em 15 s dizendo
   por quê. */

func ligarWAL(db *sql.DB) error {
	prazo := time.Now().Add(15 * time.Second)
	espera := 10 * time.Millisecond
	for {
		var modo string
		err := db.QueryRow(`PRAGMA journal_mode`).Scan(&modo)
		if err == nil && strings.EqualFold(modo, "wal") {
			return nil
		}
		if err == nil {
			// Fora da trava, o que o SQLite responder vale: há sistemas de
			// arquivo sem WAL, e ali o banco sempre abriu assim mesmo.
			if err = db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&modo); err == nil {
				return nil
			}
		}
		if !bancoOcupado(err) {
			return fmt.Errorf("ligar WAL: %w", err)
		}
		if time.Now().After(prazo) {
			return fmt.Errorf("ligar WAL: o banco seguiu ocupado por 15 s — há outro processo "+
				"segurando o arquivo? (%w)", err)
		}
		time.Sleep(espera + rand.N(espera))
		if espera < 400*time.Millisecond {
			espera *= 2
		}
	}
}
