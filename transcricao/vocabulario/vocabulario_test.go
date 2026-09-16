package vocabulario

import (
	"reflect"
	"strings"
	"testing"
)

func ler(t *testing.T, nome, conteudo string) Pacote {
	t.Helper()
	p, err := Ler(nome, strings.NewReader(conteudo))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLer(t *testing.T) {
	// Como o Bloco de Notas grava: BOM no começo e \r\n no fim de cada linha.
	conteudo := bom + strings.Join([]string{
		"# pacote: agentes-ia",
		"# os nomes que o motor erra",
		"Claude Code:  Cloud   Code , Clode Code,",
		"",
		"  Gemini  ",
		": sem forma",
		"gemini: Jemini",
		"# Obs: comentário com dois-pontos não é cabeçalho",
		"Vila Rica",
	}, "\r\n")
	p := ler(t, "arquivo", conteudo)

	if p.Nome != "agentes-ia" {
		t.Errorf("nome = %q", p.Nome)
	}
	quer := []Termo{
		{Forma: "Claude Code", Variantes: []string{"Cloud Code", "Clode Code"}},
		{Forma: "Gemini"},
		{Forma: "Vila Rica"},
	}
	if !reflect.DeepEqual(p.Termos, quer) {
		t.Errorf("termos = %+v\n       queria %+v", p.Termos, quer)
	}
	if len(p.Avisos) != 2 || !strings.Contains(p.Avisos[0], "linha 6") || !strings.Contains(p.Avisos[1], "linha 7") {
		t.Errorf("avisos = %q", p.Avisos)
	}

	if sem := ler(t, "pessoal", "seu João\ncalhas"); sem.Nome != "pessoal" || len(sem.Termos) != 2 {
		t.Errorf("sem cabeçalho o nome é o do arquivo: %+v", sem)
	}
}

func TestComporPoeOMaisImportanteNoFim(t *testing.T) {
	base := ler(t, "base", "Claude Code\nGPT\nGemini")
	oficio := ler(t, "corretor", "CRECI\nITBI")
	pessoal := ler(t, "pessoal", "seu João\ncalhas\ngpt") // gpt repetido: fica com o peso do pessoal
	conversa := Pacote{Nome: "conversa", Termos: []Termo{{Forma: "João Calhas"}}}

	d := Compor(600, base, oficio, pessoal, conversa)
	if quer := "Gemini, Claude Code, ITBI, CRECI, gpt, calhas, seu João, João Calhas"; d.Texto != quer {
		t.Errorf("dica = %q\n  queria %q", d.Texto, quer)
	}
	if len(d.Cortados) != 0 {
		t.Errorf("cortados = %q", d.Cortados)
	}
}

func TestComporCortaOMenosImportantePrimeiro(t *testing.T) {
	base := ler(t, "base", "Claude Code\nGemini")
	pessoal := ler(t, "pessoal", "seu João\ncalhas")

	// "calhas, seu João" são 17 bytes (o ã conta 2); ", Claude Code" já não cabe em 20.
	d := Compor(20, base, pessoal)
	if d.Texto != "calhas, seu João" {
		t.Errorf("dica = %q", d.Texto)
	}
	if !reflect.DeepEqual(d.Cortados, []string{"Claude Code", "Gemini"}) {
		t.Errorf("cortados = %q", d.Cortados)
	}
	if len(d.Texto) > 20 {
		t.Errorf("passou do orçamento: %d bytes", len(d.Texto))
	}

	if vazio := Compor(0, base); vazio.Texto != "" || len(vazio.Cortados) != 2 {
		t.Errorf("orçamento zero = %+v", vazio)
	}
}

func TestCorrigir(t *testing.T) {
	base := ler(t, "base", "Claude Code: Cloud Code\nClaude: Clóvis\nChatGPT: Chat GPT\nGPT-4: GPT")
	oficio := ler(t, "corretor", "matrícula: matricula\nClaude Code: Clode Code")
	c := NovoCorretor(base, oficio)

	casos := []struct{ entrada, quer string }{
		{"viu, Cloud Code?", "viu, Claude Code?"},
		{"abre o cloud   code aí", "abre o Claude Code aí"},
		{"CLOUD CODE", "Claude Code"},
		{"o Cloud Codex é outro", "o Cloud Codex é outro"},
		{"sobe no Google Cloud", "sobe no Google Cloud"},
		{"MeuCloud Code", "MeuCloud Code"},
		{"a Matricula do imóvel", "a matrícula do imóvel"},
		{"pergunta pro Chat GPT", "pergunta pro ChatGPT"},   // o trecho mais longo vence o "GPT"
		{"Cloud Code e o Clóvis", "Claude Code e o Claude"}, // uma passada só
		{"o Clode Code travou", "o Claude Code travou"},     // regra de outro pacote
		{"nada para corrigir aqui", "nada para corrigir aqui"},
	}
	for _, caso := range casos {
		if got := c.Corrigir(caso.entrada); got != caso.quer {
			t.Errorf("Corrigir(%q) = %q, queria %q", caso.entrada, got, caso.quer)
		}
	}

	var nulo *Corretor
	if got := nulo.Corrigir("Cloud Code"); got != "Cloud Code" {
		t.Errorf("corretor nil mexeu no texto: %q", got)
	}
}

// Com "B: A" e "C: B", o A vira B e para aí: a troca feita não entra na regra seguinte.
func TestCorrecaoNaoEmCascata(t *testing.T) {
	c := NovoCorretor(ler(t, "p", "Beta: Alfa\nGama: Beta"))
	if got := c.Corrigir("Alfa e Beta"); got != "Beta e Gama" {
		t.Errorf("Corrigir = %q, queria %q", got, "Beta e Gama")
	}
}

func TestCorretorMaisImportanteVence(t *testing.T) {
	base := ler(t, "base", "Claude: Cloud")
	pessoal := ler(t, "pessoal", "Google Cloud: Cloud")
	if got := NovoCorretor(base, pessoal).Corrigir("Cloud"); got != "Google Cloud" {
		t.Errorf("o mesmo erro em dois pacotes devia seguir o mais importante: %q", got)
	}
}

func TestAssinatura(t *testing.T) {
	a := ler(t, "a", "Claude Code: Cloud Code\nGemini")
	b := ler(t, "b", "ChatGPT: Chat GPT")
	um := NovoCorretor(a, b).Assinatura()
	if dois := NovoCorretor(a, b).Assinatura(); dois != um {
		t.Error("as mesmas regras deram assinaturas diferentes")
	}
	semTermoNovo := ler(t, "a", "Claude Code: Cloud Code\nGemini\nCopilot")
	if NovoCorretor(semTermoNovo, b).Assinatura() != um {
		t.Error("termo sem correção não muda regra nenhuma, e mudou a assinatura")
	}
	comErroNovo := ler(t, "a", "Claude Code: Cloud Code, Clode Code\nGemini")
	if NovoCorretor(comErroNovo, b).Assinatura() == um {
		t.Error("um erro novo tem que mudar a assinatura")
	}
	var nulo *Corretor
	if nulo.Assinatura() == "" {
		t.Error("assinatura vazia")
	}
}
