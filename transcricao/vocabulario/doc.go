/*
Package vocabulario ajuda um motor de transcrição com os nomes que ele não
adivinharia: monta a dica que vai junto com o áudio e corrige, no texto que
volta, os erros já conhecidos.

A dica de um motor Whisper é pequena — só os últimos 224 tokens contam, uns 600
caracteres —, e uma lista longa dilui o efeito e, em trecho de silêncio, pode
fazer o motor escrever palavras que ninguém disse. Por isso o pacote pensa em
ORÇAMENTO: [Compor] recebe pacotes do menos para o mais importante, corta
primeiro os de menor peso e põe o mais importante no fim da dica.

O formato do arquivo, lido por [Ler]:

	# pacote: agentes-ia
	Claude Code: Cloud Code
	ChatGPT: Chat GPT
	Gemini

Uma forma por linha; depois dos dois-pontos, separados por vírgula, os erros
que o motor comete com ela. # comenta, e "# pacote:" dá o nome. [Corretor]
troca esses erros pela forma certa, só como palavra inteira e numa passada só.

Erro conhecido que também é palavra de verdade não deve entrar: "Cloud" sozinho
existe (Google Cloud); "Cloud Code" não. O que a correção não pega, a dica
costuma evitar — e quem lê a transcrição precisa saber que ela é de máquina.

Estabilidade: v0. Só a biblioteca padrão.

In English: package vocabulario builds a budgeted speech-to-text prompt from
prioritized term packs (most important terms last, where Whisper weighs them
most) and applies whole-word corrections for known misrecognitions.
*/
package vocabulario
