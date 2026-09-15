package vocabulario_test

import (
	"fmt"
	"strings"

	"github.com/kapstanhq/whatsapp-reader/transcricao/vocabulario"
)

// Três fontes, do menos para o mais importante: a dica sai com o nome do
// contato no fim, e o texto que volta do motor é corrigido.
func Example() {
	base, _ := vocabulario.Ler("base", strings.NewReader("Claude Code: Cloud Code\nGemini"))
	pessoal, _ := vocabulario.Ler("pessoal", strings.NewReader("calhas\nrufo"))
	conversa := vocabulario.Pacote{Nome: "conversa", Termos: []vocabulario.Termo{{Forma: "seu João"}}}

	dica := vocabulario.Compor(600, base, pessoal, conversa)
	fmt.Println(dica.Texto)

	corretor := vocabulario.NovoCorretor(base, pessoal)
	fmt.Println(corretor.Corrigir("pergunta pro Cloud Code quanto de rufo"))
	// Output:
	// Gemini, Claude Code, rufo, calhas, seu João
	// pergunta pro Claude Code quanto de rufo
}
