package vocabulario

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Termo é a forma certa de uma palavra ou expressão, e os erros que o motor
// costuma cometer com ela.
type Termo struct {
	Forma     string
	Variantes []string
}

// Pacote é um conjunto de termos com nome: um arquivo de vocabulário.
type Pacote struct {
	Nome   string
	Termos []Termo
	// Avisos são as linhas que não deu para aproveitar, com o número. O resto
	// do pacote vale do mesmo jeito.
	Avisos []string
}

const separador = ", "

// O Bloco de Notas grava UTF-8 com BOM.
const bom = string(rune(0xFEFF))

// Ler interpreta um arquivo de vocabulário (formato no doc do pacote). Sem
// cabeçalho "# pacote:", o nome do pacote é nome. Linha que não dá para
// aproveitar vira aviso, não erro: um arquivo escrito à mão não pode derrubar
// a transcrição por uma vírgula. O erro devolvido é só o de leitura.
func Ler(nome string, r io.Reader) (Pacote, error) {
	p := Pacote{Nome: nome}
	vistos := map[string]bool{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		linha := sc.Text()
		if n == 1 {
			linha = strings.TrimPrefix(linha, bom)
		}
		linha = strings.TrimSpace(linha)
		if linha == "" {
			continue
		}
		if comentario, ok := strings.CutPrefix(linha, "#"); ok {
			if chave, valor, ok := strings.Cut(comentario, ":"); ok && strings.EqualFold(strings.TrimSpace(chave), "pacote") {
				if v := strings.TrimSpace(valor); v != "" {
					p.Nome = v
				}
			}
			continue
		}
		forma, erros, _ := strings.Cut(linha, ":")
		forma = normalizar(forma)
		switch {
		case forma == "":
			p.Avisos = append(p.Avisos, fmt.Sprintf("linha %d: falta a forma certa antes dos dois-pontos", n))
			continue
		case vistos[strings.ToLower(forma)]:
			p.Avisos = append(p.Avisos, fmt.Sprintf("linha %d: %q já apareceu antes", n, forma))
			continue
		}
		vistos[strings.ToLower(forma)] = true
		t := Termo{Forma: forma}
		for _, v := range strings.Split(erros, ",") {
			if v = normalizar(v); v != "" {
				t.Variantes = append(t.Variantes, v)
			}
		}
		p.Termos = append(p.Termos, t)
	}
	return p, sc.Err()
}

func normalizar(s string) string { return strings.Join(strings.Fields(s), " ") }

// Dica é o que vai para o motor, e o que ficou de fora dele.
type Dica struct {
	Texto    string   // as formas separadas por vírgula, a mais importante no fim
	Cortados []string // as que não couberam, da mais para a menos importante
}

// Compor junta as formas dos pacotes numa dica de até orcamento bytes.
//
// Os pacotes vêm do menos para o mais importante; dentro de cada um, vale a
// ordem do arquivo — a primeira linha pesa mais. Forma repetida conta uma vez,
// com o peso do pacote mais importante. Quando não cabe tudo, saem primeiro os
// termos menos importantes. Na dica a ordem se inverte: o mais importante vai
// no fim, que é o pedaço que o Whisper aproveita de uma dica longa e o que fica
// mais perto do áudio.
func Compor(orcamento int, pacotes ...Pacote) Dica {
	var ordem []string // do mais para o menos importante
	vistos := map[string]bool{}
	for i := len(pacotes) - 1; i >= 0; i-- {
		for _, t := range pacotes[i].Termos {
			if chave := strings.ToLower(t.Forma); !vistos[chave] {
				vistos[chave] = true
				ordem = append(ordem, t.Forma)
			}
		}
	}

	var d Dica
	var escolhidos []string
	tamanho := 0
	for i, forma := range ordem {
		custo := len(forma)
		if len(escolhidos) > 0 {
			custo += len(separador)
		}
		if tamanho+custo > orcamento {
			d.Cortados = ordem[i:]
			break
		}
		escolhidos = append(escolhidos, forma)
		tamanho += custo
	}
	slices.Reverse(escolhidos)
	d.Texto = strings.Join(escolhidos, separador)
	return d
}

// Corretor troca, no texto transcrito, os erros conhecidos pela forma certa.
// O zero e o nil não corrigem nada.
type Corretor struct {
	regras []regra
}

type regra struct {
	erro   string // como veio do arquivo, em minúsculas: a chave e o desempate
	padrao *regexp.Regexp
	forma  string
}

// NovoCorretor monta as regras dos pacotes, do menos para o mais importante: se
// dois pacotes corrigem o mesmo erro para formas diferentes, vale o mais
// importante.
func NovoCorretor(pacotes ...Pacote) *Corretor {
	porErro := map[string]regra{}
	for _, p := range pacotes {
		for _, t := range p.Termos {
			for _, v := range t.Variantes {
				palavras := strings.Fields(v)
				for i, w := range palavras {
					palavras[i] = regexp.QuoteMeta(w)
				}
				chave := strings.ToLower(v)
				porErro[chave] = regra{
					erro:   chave,
					padrao: regexp.MustCompile(`(?i)` + strings.Join(palavras, `\s+`)),
					forma:  t.Forma,
				}
			}
		}
	}
	c := &Corretor{}
	for _, r := range porErro {
		c.regras = append(c.regras, r)
	}
	sort.Slice(c.regras, func(i, j int) bool { return c.regras[i].erro < c.regras[j].erro })
	return c
}

// Corrigir troca cada erro conhecido pela forma certa, só quando ele aparece
// como palavra inteira: "Cloud Code" vira "Claude Code", "Cloud Codex" fica.
// Maiúsculas e espaços a mais não importam. As trocas são decididas sobre o
// texto original, numa passada só — uma correção nunca é corrigida de novo por
// outra regra —, e onde dois erros se sobrepõem vale o trecho mais longo.
func (c *Corretor) Corrigir(texto string) string {
	if c == nil || len(c.regras) == 0 {
		return texto
	}
	type troca struct {
		ini, fim int
		forma    string
	}
	var trocas []troca
	for _, r := range c.regras {
		for _, loc := range r.padrao.FindAllStringIndex(texto, -1) {
			if palavraInteira(texto, loc[0], loc[1]) {
				trocas = append(trocas, troca{loc[0], loc[1], r.forma})
			}
		}
	}
	if len(trocas) == 0 {
		return texto
	}
	sort.SliceStable(trocas, func(i, j int) bool {
		if trocas[i].ini != trocas[j].ini {
			return trocas[i].ini < trocas[j].ini
		}
		return trocas[i].fim > trocas[j].fim
	})
	var b strings.Builder
	pos := 0
	for _, t := range trocas {
		if t.ini < pos {
			continue // cai dentro de uma troca já feita
		}
		b.WriteString(texto[pos:t.ini])
		b.WriteString(t.forma)
		pos = t.fim
	}
	b.WriteString(texto[pos:])
	return b.String()
}

// Assinatura muda quando muda alguma regra, e só então. Serve para saber se
// transcrições antigas precisam ser corrigidas de novo.
func (c *Corretor) Assinatura() string {
	h := sha256.New()
	if c != nil {
		for _, r := range c.regras {
			io.WriteString(h, r.erro+"\x00"+r.forma+"\n")
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// palavraInteira diz se texto[ini:fim] não começa nem termina no meio de uma palavra.
func palavraInteira(texto string, ini, fim int) bool {
	if ini > 0 {
		if r, _ := utf8.DecodeLastRuneInString(texto[:ini]); letraOuDigito(r) {
			return false
		}
	}
	if fim < len(texto) {
		if r, _ := utf8.DecodeRuneInString(texto[fim:]); letraOuDigito(r) {
			return false
		}
	}
	return true
}

func letraOuDigito(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
