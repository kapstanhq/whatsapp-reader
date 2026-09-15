package main

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/kapstanhq/whatsapp-reader/transcricao/vocabulario"
)

/* O VOCABULÁRIO da transcrição: de onde vêm os nomes da dica, e em que ordem
   de importância — da menor para a maior.

     base       embutido na ponte: agentes de IA e WhatsApp
     pacotes    vocabulario.d/*.txt, instalados por plugin ou skill
     pessoal    vocabulario.txt, escrito à mão
     conversa   o nome do contato e o de quem usa a ponte, tirados do banco

   Quando a dica não cabe, o corte começa pelo base. Pacote de ofício (corretor
   de imóveis, por exemplo) NÃO mora aqui: a ponte é genérica, e quem traz o
   vocabulário do ofício é o plugin que o usa, com `vocabulario instalar`.

   Tudo é lido de novo a cada áudio. São arquivos pequenos, e acrescentar um
   nome precisa valer sem reiniciar o daemon. */

//go:embed vocabularios/base.txt
var pacoteBase string

const (
	arquivoVocabulario = "vocabulario.txt"
	pastaVocabularios  = "vocabulario.d"
	// Uns 600 bytes cabem nos 224 tokens de dica que o Whisper aproveita.
	orcamentoDica = 600
)

type fonte struct {
	pacote vocabulario.Pacote
	origem string // "embutido", ou o caminho do arquivo
}

// fontesDeVocabulario lê base, pacotes e pessoal, do menos para o mais
// importante. Um pacote instalado com o nome do embutido o substitui, no mesmo
// lugar da fila: é assim que o base se edita ou se desliga.
func fontesDeVocabulario(dir string) []fonte {
	base, _ := vocabulario.Ler("base", strings.NewReader(pacoteBase))
	fontes := []fonte{{base, "embutido"}}

	pasta := filepath.Join(dir, pastaVocabularios)
	entradas, _ := os.ReadDir(pasta) // em ordem de nome
	for _, e := range entradas {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".txt") {
			continue
		}
		f, ok := lerFonte(filepath.Join(pasta, e.Name()), strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		switch {
		case !ok:
		case f.pacote.Nome == base.Nome:
			fontes[0] = f
		default:
			fontes = append(fontes, f)
		}
	}
	if f, ok := lerFonte(filepath.Join(dir, arquivoVocabulario), "pessoal"); ok {
		fontes = append(fontes, f)
	}
	return fontes
}

func lerFonte(caminho, nome string) (fonte, bool) {
	arq, err := os.Open(caminho)
	if err != nil {
		return fonte{}, false // não existir é o caso comum
	}
	defer arq.Close()
	p, err := vocabulario.Ler(nome, arq)
	return fonte{p, caminho}, err == nil
}

func pacotesDe(fontes []fonte) []vocabulario.Pacote {
	ps := make([]vocabulario.Pacote, len(fontes))
	for i, f := range fontes {
		ps[i] = f.pacote
	}
	return ps
}

// vocabularioDa devolve a dica e o corretor de uma transcrição. Sem banco ou
// sem conversa — o `verificar` com um arquivo solto —, a dica sai sem os nomes.
func vocabularioDa(ctx context.Context, dir string, b *Banco, conversa string) (vocabulario.Dica, *vocabulario.Corretor) {
	ps := pacotesDe(fontesDeVocabulario(dir))
	corretor := vocabulario.NovoCorretor(ps...)
	if b != nil && conversa != "" {
		ps = append(ps, b.pacoteDaConversa(ctx, conversa))
	}
	return vocabulario.Compor(orcamentoDica, ps...), corretor
}

// Os nomes que a ponte já sabe sem ninguém escrever: o do contato e o de quem
// usa a ponte. Um áudio do seu João costuma ter "seu João" dentro.
func (b *Banco) pacoteDaConversa(ctx context.Context, conversa string) vocabulario.Pacote {
	p := vocabulario.Pacote{Nome: "conversa"}
	var contato string
	b.db.QueryRowContext(ctx, `SELECT COALESCE(nome, '') FROM conversas WHERE jid = ?`, conversa).Scan(&contato)
	proprio, _ := b.LerEstado(ctx, "nome")
	for _, nome := range []string{contato, proprio} {
		if n := nomeFalado(nome); n != "" {
			p.Termos = append(p.Termos, vocabulario.Termo{Forma: n})
		}
	}
	return p
}

// nomeFalado limpa um nome de agenda para virar termo da dica: emoji e símbolo
// saem, e o que sobra só com número (um telefone) não é palavra.
func nomeFalado(nome string) string {
	var b strings.Builder
	for _, r := range nome {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" -'.", r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	n := strings.Join(strings.Fields(b.String()), " ")
	if !strings.ContainsFunc(n, unicode.IsLetter) {
		return ""
	}
	return n
}

/* Um erro novo no vocabulário — um pacote instalado, uma linha no
   vocabulario.txt — corrige também o que já foi transcrito, a partir do texto
   bruto que o motor entregou. A assinatura das regras fica no banco: as
   transcrições só são relidas quando ela muda, e reiniciar não refaz nada. */

func (e *Esteira) recorrigirSeMudou(ctx context.Context) {
	corretor := vocabulario.NovoCorretor(pacotesDe(fontesDeVocabulario(e.dir))...)
	assinatura := corretor.Assinatura()
	if anterior, _ := e.banco.LerEstado(ctx, "vocabulario_regras"); anterior == assinatura {
		return
	}
	rows, err := e.banco.db.QueryContext(ctx, `
		SELECT mensagem, conversa, bruto, COALESCE(texto, '') FROM transcricoes
		 WHERE estado = 'feita' AND bruto IS NOT NULL`)
	if err != nil {
		return // a próxima rodada tenta de novo
	}
	type mudanca struct{ mensagem, conversa, texto string }
	var mudancas []mudanca
	for rows.Next() {
		var m mudanca
		var bruto, atual string
		if rows.Scan(&m.mensagem, &m.conversa, &bruto, &atual) == nil {
			if m.texto = corretor.Corrigir(bruto); m.texto != atual {
				mudancas = append(mudancas, m)
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, m := range mudancas {
		e.escrever(`UPDATE transcricoes SET texto = ? WHERE mensagem = ? AND conversa = ?`, m.texto, m.mensagem, m.conversa)
	}
	e.banco.Anotar(ctx, "vocabulario_regras", assinatura)
	if len(mudancas) > 0 {
		fmt.Printf("· o vocabulário mudou: %d transcrições corrigidas de novo\n", len(mudancas))
	}
}

// -- o subcomando ------------------------------------------------------------

/* `whatsapp-reader vocabulario`: o que vai na dica, de onde veio cada pedaço e
   o que ficou de fora. Existe porque o vocabulário é invisível por natureza —
   um termo cortado pelo orçamento, ou um pacote com linha quebrada, só
   apareceria como uma transcrição que continuou errando.

   `instalar` e `remover` são a porta dos plugins: quem traz um pacote de ofício
   não precisa saber onde fica a pasta da ponte. */

func Vocabulario(dir string, args []string) error {
	return comandoVocabulario(context.Background(), os.Stdout, dir, args)
}

func comandoVocabulario(ctx context.Context, w io.Writer, dir string, args []string) error {
	var busca string
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "instalar":
		return instalarVocabulario(w, dir, args[1])
	case len(args) == 2 && args[0] == "remover":
		return removerVocabulario(w, dir, args[1])
	case len(args) == 2 && args[0] == "--conversa":
		busca = args[1]
	default:
		return errors.New("uso: whatsapp-reader vocabulario [--conversa <nome>] | instalar <arquivo.txt> | remover <pacote>")
	}

	fontes := fontesDeVocabulario(dir)
	fmt.Fprintln(w, "pacotes, do menos para o mais importante:")
	for _, f := range fontes {
		fmt.Fprintf(w, "  %-20s %3d termos · %2d correções · %s\n", f.pacote.Nome, len(f.pacote.Termos), correcoes(f.pacote), f.origem)
		for _, a := range f.pacote.Avisos {
			fmt.Fprintf(w, "  %-20s !! %s\n", "", a)
		}
	}
	ps := pacotesDe(fontes)
	if busca != "" {
		b, err := AbrirBanco(dirBanco(dir))
		if err != nil {
			return fmt.Errorf("abrir banco: %w", err)
		}
		defer b.Fechar()
		cs, err := b.ListarConversas(ctx, busca, 1)
		if err != nil {
			return err
		}
		if len(cs) == 0 {
			return fmt.Errorf("nenhuma conversa com %q", busca)
		}
		conversa := b.pacoteDaConversa(ctx, cs[0].JID)
		fmt.Fprintf(w, "  %-20s %3d termos ·  0 correções · %s (%s)\n", conversa.Nome, len(conversa.Termos), cs[0].Nome, cs[0].JID)
		ps = append(ps, conversa)
	}

	d := vocabulario.Compor(orcamentoDica, ps...)
	fmt.Fprintf(w, "\ndica (%d de %d bytes; o mais importante vai no fim):\n  %s\n", len(d.Texto), orcamentoDica, cmp.Or(d.Texto, "(vazia)"))
	if len(d.Cortados) > 0 {
		fmt.Fprintf(w, "\nnão couberam (%d): %s\n", len(d.Cortados), strings.Join(d.Cortados, ", "))
	}
	fmt.Fprintln(w, "\ncorreções:")
	nenhuma := true
	for _, f := range fontes {
		for _, t := range f.pacote.Termos {
			for _, v := range t.Variantes {
				fmt.Fprintf(w, "  %s → %s  (%s)\n", v, t.Forma, f.pacote.Nome)
				nenhuma = false
			}
		}
	}
	if nenhuma {
		fmt.Fprintln(w, "  (nenhuma)")
	}
	return nil
}

func instalarVocabulario(w io.Writer, dir, caminho string) error {
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return err
	}
	p, err := vocabulario.Ler(strings.TrimSuffix(filepath.Base(caminho), filepath.Ext(caminho)), bytes.NewReader(dados))
	if err != nil {
		return err
	}
	nome := arquivoDePacote(p.Nome)
	switch {
	case nome == "":
		return fmt.Errorf("%s: o pacote %q não tem um nome que sirva de arquivo", caminho, p.Nome)
	case len(p.Termos) == 0:
		return fmt.Errorf("%s não tem nenhum termo (para desligar um pacote, use remover)", caminho)
	}
	for _, a := range p.Avisos {
		fmt.Fprintf(w, "!! %s\n", a)
	}
	destino := filepath.Join(dir, pastaVocabularios, nome+".txt")
	if err := gravarPacote(destino, dados); err != nil {
		return err
	}
	fmt.Fprintf(w, "instalado o pacote %s: %d termos, %d correções → %s\n", p.Nome, len(p.Termos), correcoes(p), destino)
	return nil
}

func removerVocabulario(w io.Writer, dir, pacote string) error {
	destino := filepath.Join(dir, pastaVocabularios, arquivoDePacote(pacote)+".txt")
	if arquivoDePacote(pacote) == "base" {
		// O base mora no binário: desligar é pôr um vazio com o mesmo nome por cima.
		vazio := "# pacote: base\n# Desligado por `whatsapp-reader vocabulario remover base`.\n# Apague este arquivo para voltar ao base embutido.\n"
		if err := gravarPacote(destino, []byte(vazio)); err != nil {
			return err
		}
		fmt.Fprintf(w, "o pacote base foi desligado; apague %s para religar\n", destino)
		return nil
	}
	if err := os.Remove(destino); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("não há pacote %q instalado em %s", pacote, filepath.Dir(destino))
	} else if err != nil {
		return err
	}
	fmt.Fprintf(w, "removido o pacote %s\n", pacote)
	return nil
}

// Pela metade não: o daemon lê a pasta a cada áudio.
func gravarPacote(destino string, dados []byte) error {
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		return err
	}
	parcial := destino + ".parcial"
	if err := os.WriteFile(parcial, dados, 0o644); err != nil {
		return err
	}
	return os.Rename(parcial, destino)
}

// "Corretor de Imóveis" → "corretor-de-imóveis": minúsculas, letras e números,
// o resto vira um hífen só.
func arquivoDePacote(nome string) string {
	var b strings.Builder
	hifen := false
	for _, r := range strings.ToLower(nome) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			hifen = false
		} else if !hifen {
			b.WriteRune('-')
			hifen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func correcoes(p vocabulario.Pacote) int {
	n := 0
	for _, t := range p.Termos {
		n += len(t.Variantes)
	}
	return n
}
