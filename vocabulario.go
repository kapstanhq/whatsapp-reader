package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

/* O VOCABULÁRIO que o motor não adivinha: o nome do pedreiro, da rua, do
   condomínio. Um termo por linha, # comenta — o mesmo jeito do
   nao-contatar.txt. Vira a dica de cada transcrição.

   É lido a cada áudio, e não na subida: o arquivo é pequeno, e acrescentar um
   nome precisa valer sem reiniciar o daemon. */

const arquivoVocabulario = "vocabulario.txt"

// O Whisper aproveita só o fim de uma dica longa (metade do contexto de texto,
// 224 tokens) e descarta o resto sem avisar. Cortando aqui, ficam os termos do
// começo do arquivo, que são os que a pessoa pôs primeiro.
const limiteDica = 600

// O Bloco de Notas grava UTF-8 com BOM.
const bom = string(rune(0xFEFF))

func lerVocabulario(dir string) string {
	dados, err := os.ReadFile(filepath.Join(dir, arquivoVocabulario))
	if err != nil {
		return "" // não existir é o caso comum
	}
	var termos []string
	tamanho := 0
	for _, linha := range strings.Split(strings.TrimPrefix(string(dados), bom), "\n") {
		linha = strings.TrimSpace(linha) // leva junto o \r do fim de linha do Windows
		if linha == "" || strings.HasPrefix(linha, "#") || slices.Contains(termos, linha) {
			continue
		}
		if tamanho+len(linha)+2 > limiteDica {
			break
		}
		termos = append(termos, linha)
		tamanho += len(linha) + 2
	}
	return strings.Join(termos, ", ")
}
