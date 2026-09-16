package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kapstanhq/whatsapp-reader/transcricao/openai"
	"github.com/kapstanhq/whatsapp-reader/transcricao/whispercpp"
)

func ambiente(vars map[string]string) func(string) string {
	return func(nome string) string { return vars[nome] }
}

func TestMontarMotorLocalPorPadrao(t *testing.T) {
	dir := t.TempDir()
	m := montarMotor(dir, ambiente(nil))
	if _, ok := m.motor.(*whispercpp.Motor); !ok || m.modo != modoLocal {
		t.Fatalf("sem configuração devia ser whisper.cpp local: %+v", m)
	}
	if quer := filepath.Join(dir, "modelos", "ggml-large-v3-turbo-q5_0.bin"); m.modelo != quer {
		t.Errorf("modelo = %q, queria o preferido %q", m.modelo, quer)
	}
	if m.whisper != "whisper-cli" || m.ffmpeg != "ffmpeg" || m.idioma != "pt" || m.problema != "" {
		t.Errorf("padrões = %+v", m)
	}
	if m.descricao != "whisper.cpp local · large-v3-turbo-q5_0 · nada sai desta máquina" {
		t.Errorf("descrição = %q", m.descricao)
	}
}

func TestMontarMotorAchaOModeloBaixado(t *testing.T) {
	casos := []struct {
		nome     string
		arquivos []string
		env      map[string]string
		quer     string // relativo a <dir>/modelos, ou absoluto quando vem do ambiente
	}{
		{"só o leve", []string{"ggml-small-q5_1.bin"}, nil, "ggml-small-q5_1.bin"},
		{"os dois: o preferido", []string{"ggml-small-q5_1.bin", "ggml-large-v3-turbo-q5_0.bin"}, nil, "ggml-large-v3-turbo-q5_0.bin"},
		{"um que a ponte não conhece", []string{"ggml-medium.bin", "leia-me.txt"}, nil, "ggml-medium.bin"},
		{"o ambiente manda", []string{"ggml-small-q5_1.bin"},
			map[string]string{"WHATSAPP_READER_WHISPER_MODELO": "D:/modelos/ggml-base.bin"}, "D:/modelos/ggml-base.bin"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			dir := t.TempDir()
			pasta := filepath.Join(dir, "modelos")
			os.MkdirAll(pasta, 0o755)
			for _, a := range c.arquivos {
				os.WriteFile(filepath.Join(pasta, a), []byte("ggml"), 0o600)
			}
			quer := c.quer
			if !strings.Contains(quer, "/") {
				quer = filepath.Join(pasta, quer)
			}
			if m := montarMotor(dir, ambiente(c.env)); m.modelo != quer {
				t.Errorf("modelo = %q, queria %q", m.modelo, quer)
			}
		})
	}
}

func TestMontarMotorAPI(t *testing.T) {
	m := montarMotor(t.TempDir(), ambiente(map[string]string{
		"WHATSAPP_READER_TRANSCRICAO": " API ",
		"WHATSAPP_READER_API_URL":     "https://api.groq.com/openai/v1",
		"WHATSAPP_READER_API_CHAVE":   "gsk_nao_pode_aparecer",
		"WHATSAPP_READER_API_MODELO":  "whisper-large-v3-turbo",
		"WHATSAPP_READER_IDIOMA":      "es",
	}))
	if _, ok := m.motor.(*openai.Motor); !ok || m.modo != modoAPI || !m.temChave || m.idioma != "es" {
		t.Fatalf("motor = %+v", m)
	}
	if m.descricao != "api.groq.com · whisper-large-v3-turbo · os áudios SAEM desta máquina" {
		t.Errorf("descrição = %q", m.descricao)
	}
}

func TestMontarMotorNaoPegaAChaveDeOutroPrograma(t *testing.T) {
	m := montarMotor(t.TempDir(), ambiente(map[string]string{
		"WHATSAPP_READER_TRANSCRICAO": "api",
		"WHATSAPP_READER_API_URL":     "https://api.openai.com/v1",
		"OPENAI_API_KEY":              "sk-do-ambiente",
	}))
	if m.temChave {
		t.Error("a chave de outro programa não pode virar a da ponte")
	}
	if m.descricao != "api.openai.com · os áudios SAEM desta máquina" {
		t.Errorf("descrição = %q", m.descricao)
	}
}

func TestMontarMotorDesligada(t *testing.T) {
	for valor, comProblema := range map[string]bool{"desligada": false, "whisper": true} {
		m := montarMotor(t.TempDir(), ambiente(map[string]string{"WHATSAPP_READER_TRANSCRICAO": valor}))
		if m.motor != nil || m.modo != modoDesligada || !strings.HasPrefix(m.descricao, "DESLIGADA") {
			t.Errorf("%s: %+v", valor, m)
		}
		if (m.problema != "") != comProblema {
			t.Errorf("%s: problema = %q", valor, m.problema)
		}
	}
}
