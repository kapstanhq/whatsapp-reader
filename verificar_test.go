package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPecasDoMotorLocalSemNadaInstalado(t *testing.T) {
	mt := montarMotor(t.TempDir(), ambiente(nil))
	nada := func(string) (string, error) { return "", exec.ErrNotFound }

	for goos, quer := range map[string][2]string{
		"windows": {"scoop install whisper-cpp", "scoop install ffmpeg"},
		"darwin":  {"brew install whisper-cpp", "brew install ffmpeg"},
		"linux":   {"github.com/ggml-org/whisper.cpp", "apt install ffmpeg"},
	} {
		pecas := pecasDoMotor(mt, nada, goos)
		if len(pecas) != 3 {
			t.Fatalf("%s: %d peças", goos, len(pecas))
		}
		for _, p := range pecas {
			if p.ok || p.dica == "" {
				t.Errorf("%s: %s devia faltar, com dica: %+v", goos, p.nome, p)
			}
		}
		if !strings.Contains(pecas[0].dica, quer[0]) || !strings.Contains(pecas[1].dica, quer[1]) {
			t.Errorf("%s: dicas = %q / %q", goos, pecas[0].dica, pecas[1].dica)
		}
		if !strings.Contains(pecas[0].dica, "WHATSAPP_READER_WHISPER") {
			t.Errorf("%s: a dica devia dizer como apontar outro executável: %q", goos, pecas[0].dica)
		}
	}

	modelo := pecasDoMotor(mt, nada, "windows")[2]
	url := "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo-q5_0.bin"
	if !strings.Contains(modelo.dica, url) || !strings.Contains(modelo.dica, mt.modelo) || !strings.Contains(modelo.dica, "ggml-small-q5_1.bin") {
		t.Errorf("dica do modelo = %q", modelo.dica)
	}
}

func TestPecasDoMotorLocalComTudo(t *testing.T) {
	dir := t.TempDir()
	mt := montarMotor(dir, ambiente(nil))
	os.MkdirAll(filepath.Dir(mt.modelo), 0o755)
	os.WriteFile(mt.modelo, make([]byte, 3<<20), 0o600)
	achou := func(nome string) (string, error) { return `C:\Users\u\scoop\shims\` + nome + ".exe", nil }

	for _, p := range pecasDoMotor(mt, achou, "windows") {
		if !p.ok || p.dica != "" {
			t.Errorf("%s devia estar ok: %+v", p.nome, p)
		}
	}
	if onde := pecasDoMotor(mt, achou, "windows")[2].onde; !strings.Contains(onde, "3,0 MB") {
		t.Errorf("o modelo devia mostrar o tamanho: %q", onde)
	}
}

func TestPecasDoMotorAPI(t *testing.T) {
	casos := []struct {
		nome   string
		env    map[string]string
		faltam []string
	}{
		{"completo", map[string]string{
			"WHATSAPP_READER_API_URL": "https://api.groq.com/openai/v1", "WHATSAPP_READER_API_MODELO": "whisper-large-v3-turbo",
			"WHATSAPP_READER_API_CHAVE": "gsk_x"}, nil},
		{"vazio", nil, []string{"endereço", "modelo", "chave"}},
		{"servidor local sem chave", map[string]string{
			"WHATSAPP_READER_API_URL": "http://localhost:8000/v1", "WHATSAPP_READER_API_MODELO": "Systran/faster-whisper-small"}, nil},
	}
	for _, c := range casos {
		env := map[string]string{"WHATSAPP_READER_TRANSCRICAO": "api"}
		for k, v := range c.env {
			env[k] = v
		}
		var faltam []string
		for _, p := range pecasDoMotor(montarMotor(t.TempDir(), ambiente(env)), nil, "windows") {
			if !p.ok {
				faltam = append(faltam, p.nome)
			}
			if strings.Contains(p.onde, "gsk_x") {
				t.Errorf("%s: a chave apareceu: %+v", c.nome, p)
			}
		}
		if strings.Join(faltam, ",") != strings.Join(c.faltam, ",") {
			t.Errorf("%s: faltam %v, queria %v", c.nome, faltam, c.faltam)
		}
	}
}

func TestRitmo(t *testing.T) {
	casos := []struct {
		audio, levou time.Duration
		quer         string
	}{
		{42 * time.Second, 31 * time.Second, "0:42 de áudio em 0:31 (0,7× a duração)"},
		{90 * time.Second, 135 * time.Second, "1:30 de áudio em 2:15 (1,5× a duração)"},
		{0, 3400 * time.Millisecond, "transcrito em 0:03"},
	}
	for _, c := range casos {
		if got := ritmo(c.audio, c.levou); got != c.quer {
			t.Errorf("ritmo(%s, %s) = %q, queria %q", c.audio, c.levou, got, c.quer)
		}
	}
}
