package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kapstanhq/whatsapp-reader/transcricao"
	"github.com/kapstanhq/whatsapp-reader/transcricao/ffmpeg"
	"github.com/kapstanhq/whatsapp-reader/transcricao/openai"
	"github.com/kapstanhq/whatsapp-reader/transcricao/whispercpp"
)

/* O MOTOR DE TRANSCRIÇÃO, e o único lugar que lê o ambiente para montá-lo.

   Os pacotes de transcricao recebem tudo explícito; de onde vem cada valor é
   decisão do app, e mora aqui. O ambiente é lido na subida do `serve`, e o que
   o motor é — e o que falta nele — vai para o banco (`transcricao_motor`,
   `transcricao_problema`): o `mcp` roda com o ambiente do Claude Code e não
   enxerga o da janela do daemon.

   Local é o padrão porque o áudio é a voz de outras pessoas, e sair da máquina
   tem de ser escolha explícita. Pelo mesmo motivo a chave nunca vem de
   OPENAI_API_KEY só porque ela está no ambiente. */

const (
	modoLocal     = "local"
	modoAPI       = "api"
	modoDesligada = "desligada"
)

// Os modelos que a ponte procura em <dir>/modelos, do preferido para o mais leve.
var modelosConhecidos = []string{"ggml-large-v3-turbo-q5_0.bin", "ggml-small-q5_1.bin"}

type motorMontado struct {
	modo      string
	motor     transcricao.Motor // nil com a transcrição desligada
	descricao string            // o que o estado da ponte mostra
	problema  string            // configuração que nem chegou a montar um motor
	idioma    string

	// O que o `verificar` confere, já resolvido.
	whisper, ffmpeg, modelo string
	url, modeloAPI          string
	temChave                bool
}

func montarMotor(dir string, getenv func(string) string) motorMontado {
	env := func(nome string) string { return strings.TrimSpace(getenv("WHATSAPP_READER_" + nome)) }
	m := motorMontado{modo: strings.ToLower(env("TRANSCRICAO")), idioma: env("IDIOMA")}
	if m.idioma == "" {
		m.idioma = "pt"
	}
	switch m.modo {
	case "", modoLocal:
		m.modo = modoLocal
		m.whisper, m.ffmpeg, m.modelo = env("WHISPER"), env("FFMPEG"), env("WHISPER_MODELO")
		if m.whisper == "" {
			m.whisper = "whisper-cli"
		}
		if m.ffmpeg == "" {
			m.ffmpeg = "ffmpeg"
		}
		if m.modelo == "" {
			m.modelo = acharModelo(filepath.Join(dir, "modelos"))
		}
		threads, _ := strconv.Atoi(env("WHISPER_THREADS"))
		m.motor = whispercpp.Novo(whispercpp.Config{
			Executavel: m.whisper,
			Modelo:     m.modelo,
			Threads:    threads,
			Conversor:  ffmpeg.Novo(ffmpeg.Config{Executavel: m.ffmpeg, Preparar: prepararFilho}),
			DirTemp:    filepath.Join(dir, "midia", ".tmp"),
			Preparar:   prepararFilho,
		})
		m.descricao = "whisper.cpp local · " + nomeDoModelo(m.modelo) + " · nada sai desta máquina"

	case modoAPI:
		chave := env("API_CHAVE")
		m.url, m.modeloAPI, m.temChave = env("API_URL"), env("API_MODELO"), chave != ""
		m.motor = openai.Novo(openai.Config{URL: m.url, Chave: chave, Modelo: m.modeloAPI})
		partes := []string{hostDaAPI(m.url)}
		if m.modeloAPI != "" {
			partes = append(partes, m.modeloAPI)
		}
		m.descricao = strings.Join(append(partes, "os áudios SAEM desta máquina"), " · ")

	case modoDesligada:
		m.descricao = "DESLIGADA por WHATSAPP_READER_TRANSCRICAO=desligada"

	default:
		m.problema = fmt.Sprintf("WHATSAPP_READER_TRANSCRICAO=%s não existe; use local, api ou desligada", m.modo)
		m.modo = modoDesligada
		m.descricao = "DESLIGADA: " + m.problema
	}
	return m
}

// O primeiro modelo conhecido que existir; depois, qualquer ggml-*.bin da pasta.
// Sem nenhum, o caminho do preferido — é ele que o `verificar` manda baixar.
func acharModelo(pasta string) string {
	for _, nome := range modelosConhecidos {
		if st, err := os.Stat(filepath.Join(pasta, nome)); err == nil && !st.IsDir() {
			return filepath.Join(pasta, nome)
		}
	}
	entradas, _ := os.ReadDir(pasta)
	for _, e := range entradas {
		if n := e.Name(); !e.IsDir() && strings.HasPrefix(n, "ggml-") && strings.HasSuffix(n, ".bin") {
			return filepath.Join(pasta, n)
		}
	}
	return filepath.Join(pasta, modelosConhecidos[0])
}

// ".../ggml-large-v3-turbo-q5_0.bin" → "large-v3-turbo-q5_0"
func nomeDoModelo(caminho string) string {
	return strings.TrimSuffix(strings.TrimPrefix(filepath.Base(caminho), "ggml-"), ".bin")
}

func hostDaAPI(endereco string) string {
	if u, err := url.Parse(endereco); err == nil && u.Host != "" {
		return u.Host
	}
	return "API sem endereço"
}
