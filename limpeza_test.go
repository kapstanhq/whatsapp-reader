package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type cenaLimpeza struct {
	*cenario
	velho, recente int64
}

func novaCenaLimpeza(t *testing.T) *cenaLimpeza {
	c := novoCenario(t, func(cfg *configEsteira) { cfg.guardarDias = 30 })
	agora := c.rel.agora()
	return &cenaLimpeza{cenario: c, velho: agora.AddDate(0, 0, -31).Unix(), recente: agora.AddDate(0, 0, -2).Unix()}
}

// Uma mensagem de áudio apontando para midia/<sha[:2]>/<sha>.ogg; o arquivo é
// criado junto. Fora de "baixada", a linha fica sem arquivo, como na esteira.
func (c *cenaLimpeza) audio(t *testing.T, id, sha, estado string, baixadaEm int64) string {
	t.Helper()
	rel := "midia/" + sha[:2] + "/" + sha + ".ogg"
	abs := filepath.Join(c.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(abs, make([]byte, 2048), 0o600)
	var arquivo any
	if estado == midiaBaixada {
		arquivo = rel
	}
	if _, err := c.b.db.Exec(`
		INSERT INTO midias (mensagem, conversa, tipo, sha256, tamanho, em, estado, arquivo, baixada_em)
		VALUES (?, ?, 'audio', ?, 2048, ?, ?, ?, ?)`, id, joaoJID, sha, baixadaEm, estado, arquivo, baixadaEm); err != nil {
		t.Fatal(err)
	}
	return abs
}

func (c *cenaLimpeza) transcricao(id, estado string) {
	c.b.db.Exec(`INSERT INTO transcricoes (mensagem, conversa, estado, texto) VALUES (?, ?, ?, 'olha as calhas')`, id, joaoJID, estado)
}

func existe(t *testing.T, caminho string) {
	t.Helper()
	if _, err := os.Stat(caminho); err != nil {
		t.Errorf("%s devia ficar: %v", caminho, err)
	}
}

func TestLimpezaApagaSoOQueNinguemMaisPrecisa(t *testing.T) {
	c := novaCenaLimpeza(t)
	ctx := context.Background()

	// aa: encaminhado duas vezes, as duas velhas e transcritas — sai.
	sai := c.audio(t, "A1", "aa11", midiaBaixada, c.velho)
	c.audio(t, "A2", "aa11", midiaBaixada, c.velho)
	c.transcricao("A1", "feita")
	c.transcricao("A2", "feita")
	// bb: velho, mas o mesmo conteúdo chegou de novo há dois dias — fica.
	fica1 := c.audio(t, "B1", "bb22", midiaBaixada, c.velho)
	c.audio(t, "B2", "bb22", midiaBaixada, c.recente)
	// cc: velho, com a transcrição ainda por fazer — fica.
	fica2 := c.audio(t, "C1", "cc33", midiaBaixada, c.velho)
	c.transcricao("C1", "pendente")
	// dd: velho, com outra mensagem do mesmo conteúdo na fila de download — fica.
	fica3 := c.audio(t, "D1", "dd44", midiaBaixada, c.velho)
	c.audio(t, "D2", "dd44", midiaPendente, 0)
	// ee: velho, e o arquivo já tinha sumido — só o banco muda.
	os.Remove(c.audio(t, "E1", "ee55", midiaBaixada, c.velho))
	// ff: um caminho que sai da pasta midia/ — nunca.
	c.b.db.Exec(`INSERT INTO midias (mensagem, conversa, tipo, sha256, em, estado, arquivo, baixada_em)
	             VALUES ('F1', ?, 'audio', 'ff66', ?, 'baixada', 'midia/../mensagens.db', ?)`, joaoJID, c.velho, c.velho)

	n, bytes, err := c.e.limpar(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || bytes != 2048 {
		t.Errorf("apagou %d arquivos (%d bytes), esperava 1 (2048)", n, bytes)
	}
	naoExisteArquivo(t, sai)
	for _, f := range []string{fica1, fica2, fica3, filepath.Join(c.dir, "mensagens.db")} {
		existe(t, f)
	}
	for id, semArquivo := range map[string]bool{"A1": true, "A2": true, "E1": true, "B1": false, "C1": false, "D1": false, "F1": false} {
		if got := c.midia(t, id).arquivo == ""; got != semArquivo {
			t.Errorf("%s sem arquivo = %v, esperava %v", id, got, semArquivo)
		}
	}
	if s := c.transcrita(t, "A1"); s.estado != "feita" || s.texto != "olha as calhas" {
		t.Errorf("a transcrição tem que ficar: %+v", s)
	}

	// A segunda rodada não acha mais nada.
	if n, _, _ := c.e.limpar(ctx); n != 0 {
		t.Errorf("a segunda rodada apagou %d", n)
	}
}

func TestLimpezaComArquivoPresoDevolveOCaminho(t *testing.T) {
	c := novaCenaLimpeza(t)
	preso := c.audio(t, "G1", "gg77", midiaBaixada, c.velho)
	// Uma pasta com coisa dentro no lugar do arquivo: o Remove falha, como
	// falharia com um player segurando o arquivo no Windows.
	os.Remove(preso)
	os.MkdirAll(filepath.Join(preso, "dentro"), 0o755)

	if n, _, err := c.e.limpar(context.Background()); err != nil || n != 0 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	if md := c.midia(t, "G1"); md.arquivo != "midia/gg/gg77.ogg" {
		t.Errorf("o arquivo que não saiu tem que continuar no banco: %+v", md)
	}
}

func TestLimpezaDesligadaNaoApagaNada(t *testing.T) {
	c := novaCenaLimpeza(t)
	c.cfg().guardarDias = 0
	velho := c.audio(t, "A1", "aa11", midiaBaixada, c.velho)
	if n, _, err := c.e.limpar(context.Background()); n != 0 || err != nil {
		t.Errorf("n = %d, err = %v", n, err)
	}
	existe(t, velho)
}

func (c *cenaLimpeza) cfg() *configEsteira { return &c.e.cfg }

func TestDiasDeGuarda(t *testing.T) {
	for valor, esperado := range map[string]int{"": 0, "90": 90, " 30 ": 30, "0": 0, "-5": 0, "sempre": 0} {
		if n := diasDeGuarda(func(string) string { return valor }); n != esperado {
			t.Errorf("WHATSAPP_READER_MIDIA_GUARDAR_DIAS=%q deu %d, esperava %d", valor, n, esperado)
		}
	}
}
