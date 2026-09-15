package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLerVocabulario(t *testing.T) {
	dir := t.TempDir()
	if got := lerVocabulario(dir); got != "" {
		t.Errorf("sem arquivo = %q", got)
	}

	linhas := []string{"# gente e lugares", "seu João", "", "calhas", "  rufo  ", "seu João", "# fim", "Alto da Bronze"}
	conteudo := bom + strings.Join(linhas, "\r\n") // como o Bloco de Notas grava
	if err := os.WriteFile(filepath.Join(dir, arquivoVocabulario), []byte(conteudo), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, quer := lerVocabulario(dir), "seu João, calhas, rufo, Alto da Bronze"; got != quer {
		t.Errorf("vocabulário = %q, queria %q", got, quer)
	}
}

func TestVocabularioLongoCortaNoTermo(t *testing.T) {
	dir := t.TempDir()
	var linhas []string
	for i := 0; i < 200; i++ {
		linhas = append(linhas, fmt.Sprintf("termo número %d", i))
	}
	os.WriteFile(filepath.Join(dir, arquivoVocabulario), []byte(strings.Join(linhas, "\n")), 0o600)

	dica := lerVocabulario(dir)
	if len(dica) > limiteDica || len(dica) < limiteDica-40 {
		t.Errorf("dica com %d bytes: devia encher até perto de %d sem passar", len(dica), limiteDica)
	}
	for i, termo := range strings.Split(dica, ", ") {
		if termo != fmt.Sprintf("termo número %d", i) {
			t.Fatalf("termo %d = %q: devia manter os primeiros, inteiros e em ordem", i, termo)
		}
	}
}
