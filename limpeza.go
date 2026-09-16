package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

/* A RETENÇÃO: quanto tempo o arquivo de áudio fica no disco depois de baixado.

   O padrão é guardar para sempre. Um áudio apagado não volta, e o link do
   WhatsApp vence em dias. Quem liga WHATSAPP_READER_MIDIA_GUARDAR_DIAS troca
   essa volta por disco: o arquivo sai, a transcrição e a chave ficam.

   O arquivo é endereçado por conteúdo, e um áudio encaminhado é o mesmo arquivo
   para várias mensagens. Por isso a decisão é por sha256, nunca por linha: o
   arquivo só sai quando NENHUMA mensagem que aponta para ele ainda precisa
   dele — nenhuma baixada depois do corte, nenhuma na fila de download, nenhuma
   transcrição por fazer. A conferência mora dentro do UPDATE, e não numa
   consulta antes dele: a esteira roda ao mesmo tempo, e um encaminhado que
   chega no meio da limpeza não pode ficar apontando para um arquivo apagado. */

func diasDeGuarda(getenv func(string) string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(getenv("WHATSAPP_READER_MIDIA_GUARDAR_DIAS"))); err == nil && n > 0 {
		return n
	}
	return 0
}

func (e *Esteira) limparSempre(ctx context.Context) {
	defer e.wg.Done()
	// Um minuto depois de subir: a subida já tem recuperação e history sync
	// disputando o disco.
	espera := time.NewTimer(time.Minute)
	defer espera.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-espera.C:
		}
		n, bytes, err := e.limpar(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			fmt.Fprintf(os.Stderr, "!! limpeza de áudios: %v\n", err)
		case n > 0:
			fmt.Printf("· limpeza: %d áudios saíram do disco (%s); as transcrições ficam\n", n, tamanhoHumano(bytes))
		}
		espera.Reset(6 * time.Hour)
	}
}

// limpar apaga os arquivos vencidos e devolve quantos saíram e quanto liberaram.
func (e *Esteira) limpar(ctx context.Context) (apagados int, liberados int64, err error) {
	if e.cfg.guardarDias <= 0 {
		return 0, 0, nil
	}
	agora := e.agora()
	corte := agora.AddDate(0, 0, -e.cfg.guardarDias).Unix()

	type candidato struct {
		sha, arquivo string
		tamanho      int64
	}
	rows, err := e.banco.db.QueryContext(ctx, `
		SELECT sha256, MIN(arquivo), MAX(COALESCE(tamanho, 0)) FROM midias
		 WHERE arquivo IS NOT NULL AND sha256 IS NOT NULL AND COALESCE(baixada_em, 0) < ?
		 GROUP BY sha256`, corte)
	if err != nil {
		return 0, 0, err
	}
	var candidatos []candidato
	for rows.Next() {
		var c candidato
		if err := rows.Scan(&c.sha, &c.arquivo, &c.tamanho); err != nil {
			rows.Close()
			return 0, 0, err
		}
		candidatos = append(candidatos, c)
	}
	rows.Close()

	for _, c := range candidatos {
		rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(c.arquivo)))
		if !strings.HasPrefix(rel, "midia/") {
			continue // só o que a esteira escreveu: nunca um caminho que saia da pasta
		}
		res, err := e.banco.db.ExecContext(ctx, `
			UPDATE midias SET arquivo = NULL, apagada_em = ?
			 WHERE sha256 = ? AND arquivo IS NOT NULL
			   AND NOT EXISTS (SELECT 1 FROM midias o
			                    WHERE o.sha256 = ?
			                      AND (o.estado IN ('pendente', 'baixando', 'pedida')
			                           OR (o.arquivo IS NOT NULL AND COALESCE(o.baixada_em, 0) >= ?)))
			   AND NOT EXISTS (SELECT 1 FROM transcricoes t
			                     JOIN midias o ON o.mensagem = t.mensagem AND o.conversa = t.conversa
			                    WHERE o.sha256 = ? AND t.estado IN ('pendente', 'transcrevendo'))`,
			agora.Unix(), c.sha, c.sha, corte, c.sha)
		if err != nil {
			return apagados, liberados, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		// O banco primeiro: se cair entre os dois, sobra um arquivo sem dono, e
		// não uma linha apontando para o nada.
		switch err := os.Remove(filepath.Join(e.dir, filepath.FromSlash(rel))); {
		case err == nil:
			apagados++
			liberados += c.tamanho
		case errors.Is(err, fs.ErrNotExist):
		default:
			// Arquivo preso (um player com ele aberto): devolve o caminho às
			// linhas, e a próxima rodada tenta de novo.
			e.escrever(`UPDATE midias SET arquivo = ?, apagada_em = NULL
			             WHERE sha256 = ? AND arquivo IS NULL AND apagada_em = ?`, c.arquivo, c.sha, agora.Unix())
			fmt.Fprintf(os.Stderr, "!! limpeza: %s ficou: %v\n", rel, err)
		}
	}
	return apagados, liberados, nil
}
