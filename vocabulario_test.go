package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func escreverArquivo(t *testing.T, caminho, conteudo string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caminho, []byte(conteudo), 0o600); err != nil {
		t.Fatal(err)
	}
}

func nomesDas(fontes []fonte) []string {
	var nomes []string
	for _, f := range fontes {
		nomes = append(nomes, f.pacote.Nome)
	}
	return nomes
}

func TestFontesDeVocabularioEmOrdemDeImportancia(t *testing.T) {
	dir := t.TempDir()
	if got := nomesDas(fontesDeVocabulario(dir)); !reflect.DeepEqual(got, []string{"base"}) {
		t.Fatalf("sem nada instalado, só o embutido: %q", got)
	}

	escreverArquivo(t, filepath.Join(dir, pastaVocabularios, "corretor.txt"), "# pacote: corretor-imoveis\nCRECI\nITBI")
	escreverArquivo(t, filepath.Join(dir, pastaVocabularios, "leia-me.md"), "não é pacote")
	bom := string(rune(0xFEFF))
	escreverArquivo(t, filepath.Join(dir, arquivoVocabulario), bom+"seu João\r\ncalhas\r\n")

	fontes := fontesDeVocabulario(dir)
	if got := nomesDas(fontes); !reflect.DeepEqual(got, []string{"base", "corretor-imoveis", "pessoal"}) {
		t.Fatalf("fontes = %q", got)
	}
	if fontes[0].origem != "embutido" || !strings.HasSuffix(fontes[1].origem, "corretor.txt") {
		t.Errorf("origens = %q, %q", fontes[0].origem, fontes[1].origem)
	}
	base := fontes[0].pacote
	if base.Termos[0].Forma != "Claude Code" || base.Termos[0].Variantes[0] != "Cloud Code" || len(base.Avisos) != 0 {
		t.Errorf("o base embutido devia começar por Claude Code, sem avisos: %+v", base)
	}
	if p := fontes[2].pacote; len(p.Termos) != 2 || p.Termos[0].Forma != "seu João" {
		t.Errorf("pessoal = %+v", p)
	}
}

func TestPacoteInstaladoComONomeDoBaseOSubstitui(t *testing.T) {
	dir := t.TempDir()
	escreverArquivo(t, filepath.Join(dir, pastaVocabularios, "0-outro.txt"), "Gemini")
	escreverArquivo(t, filepath.Join(dir, pastaVocabularios, "meu-base.txt"), "# pacote: base\nClaude Code: Clóvis Code")

	fontes := fontesDeVocabulario(dir)
	if got := nomesDas(fontes); !reflect.DeepEqual(got, []string{"base", "0-outro"}) {
		t.Fatalf("o base substituto fica no lugar do embutido, o menos importante: %q", got)
	}
	if fontes[0].origem == "embutido" || len(fontes[0].pacote.Termos) != 1 {
		t.Errorf("base = %+v", fontes[0])
	}
}

func TestVocabularioDaConversa(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b := bancoTeste(t)
	b.GravarConversa(ctx, joaoJID, "João Calhas 🔧", time.Now())
	b.Anotar(ctx, "nome", "Jonatas")
	escreverArquivo(t, filepath.Join(dir, arquivoVocabulario), "calhas")

	dica, corretor := vocabularioDa(ctx, dir, b, joaoJID)
	if !strings.HasSuffix(dica.Texto, "calhas, Jonatas, João Calhas") || !strings.Contains(dica.Texto, "Claude Code") {
		t.Errorf("dica = %q", dica.Texto)
	}
	if got := corretor.Corrigir("viu, Cloud Code?"); got != "viu, Claude Code?" {
		t.Errorf("Corrigir = %q", got)
	}
	if solto, _ := vocabularioDa(ctx, dir, nil, ""); strings.Contains(solto.Texto, "João Calhas") {
		t.Errorf("sem conversa, a dica não tem nome de contato: %q", solto.Texto)
	}
}

func TestNomeFalado(t *testing.T) {
	casos := map[string]string{
		"João Calhas 🔧":      "João Calhas",
		"Maria (corretora)":  "Maria corretora",
		"+55 51 99999-8888":  "",
		"":                   "",
		"Dr. Paulo O'Neil":   "Dr. Paulo O'Neil",
		"  Ana   Paula  ":    "Ana Paula",
		"Síndico Alto 2 ★★★": "Síndico Alto 2",
	}
	for nome, quer := range casos {
		if got := nomeFalado(nome); got != quer {
			t.Errorf("nomeFalado(%q) = %q, queria %q", nome, got, quer)
		}
	}
}

func TestComandoVocabulario(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	var saida bytes.Buffer
	rodar := func(args ...string) error {
		saida.Reset()
		return comandoVocabulario(ctx, &saida, dir, args)
	}

	pacote := filepath.Join(t.TempDir(), "corretor.txt")
	escreverArquivo(t, pacote, "# pacote: Corretor de Imóveis\nCRECI\nITBI: I T B I\n: quebrada\n")
	if err := rodar("instalar", pacote); err != nil {
		t.Fatal(err)
	}
	instalado := filepath.Join(dir, pastaVocabularios, "corretor-de-imóveis.txt")
	if _, err := os.Stat(instalado); err != nil {
		t.Fatalf("o pacote devia estar em %s: %v", instalado, err)
	}
	if !strings.Contains(saida.String(), "!! linha 4") || !strings.Contains(saida.String(), "2 termos, 1 correções") {
		t.Errorf("instalar:\n%s", saida.String())
	}

	if err := rodar(); err != nil {
		t.Fatal(err)
	}
	for _, quer := range []string{"Corretor de Imóveis", "I T B I → ITBI", "Cloud Code → Claude Code", "dica (", "CRECI"} {
		if !strings.Contains(saida.String(), quer) {
			t.Errorf("faltou %q em:\n%s", quer, saida.String())
		}
	}

	b, err := AbrirBanco(dirBanco(dir))
	if err != nil {
		t.Fatal(err)
	}
	b.GravarConversa(ctx, joaoJID, "João Calhas", time.Now())
	b.Fechar()
	if err := rodar("--conversa", "João"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saida.String(), "João Calhas") {
		t.Errorf("--conversa devia pôr o nome do contato na dica:\n%s", saida.String())
	}

	if err := rodar("remover", "Corretor de Imóveis"); err != nil {
		t.Fatal(err)
	}
	naoExisteArquivo(t, instalado)
	if err := rodar("remover", "base"); err != nil {
		t.Fatal(err)
	}
	if f := fontesDeVocabulario(dir)[0]; f.origem == "embutido" || len(f.pacote.Termos) != 0 {
		t.Errorf("remover base devia desligar o embutido: %+v", f)
	}

	vazio := filepath.Join(t.TempDir(), "vazio.txt")
	escreverArquivo(t, vazio, "# só comentário\n")
	for _, args := range [][]string{{"remover", "nao-existe"}, {"instalar", vazio}, {"instalar", filepath.Join(dir, "sumiu.txt")}, {"listar"}} {
		if err := rodar(args...); err == nil {
			t.Errorf("%q devia dar erro", args)
		}
	}
}

func TestArquivoDePacote(t *testing.T) {
	casos := map[string]string{"Corretor de Imóveis": "corretor-de-imóveis", "  base ": "base", "a//b": "a-b", "!!!": ""}
	for nome, quer := range casos {
		if got := arquivoDePacote(nome); got != quer {
			t.Errorf("arquivoDePacote(%q) = %q, queria %q", nome, got, quer)
		}
	}
}
