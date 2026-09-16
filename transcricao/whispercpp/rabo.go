package whispercpp

import "strings"

// rabo guarda só o fim do stderr: a frase que explica a falha vem por último,
// e a whisper-cli descreve o modelo inteiro ao carregar. É o mesmo de
// transcricao/ffmpeg — vinte linhas não pagam um pacote compartilhado.
type rabo struct{ b []byte }

const tamanhoRabo = 8 << 10

func (r *rabo) Write(p []byte) (int, error) {
	r.b = append(r.b, p...)
	if len(r.b) > tamanhoRabo {
		r.b = r.b[len(r.b)-tamanhoRabo:]
	}
	return len(p), nil
}

func (r *rabo) String() string { return string(r.b) }

// resumo é a linha que vai na mensagem de erro: a última que começa com
// "error", ou a última de todas.
func (r *rabo) resumo() string {
	var ultima, erro string
	for _, l := range strings.Split(string(r.b), "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		ultima = l
		if strings.HasPrefix(strings.ToLower(l), "error") {
			erro = l
		}
	}
	if erro != "" {
		ultima = erro
	}
	if ultima == "" {
		return "sem mensagem"
	}
	if len(ultima) > 200 {
		ultima = strings.ToValidUTF8(ultima[:200], "")
	}
	return ultima
}
