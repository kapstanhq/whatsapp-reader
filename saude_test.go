package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEstadoDizComoAndaATranscricao(t *testing.T) {
	ctx := context.Background()
	b := bancoTeste(t)
	chegou := time.Now().Add(-2*time.Hour - time.Minute).Unix()
	b.db.Exec(`INSERT INTO midias (mensagem, conversa, tipo, em, estado, arquivo)
	           VALUES ('A1', ?, 'audio', ?, 'baixada', 'midia/ab/ab.ogg')`, joaoJID, chegou)
	b.db.Exec(`INSERT INTO transcricoes (mensagem, conversa) VALUES ('A1', ?)`, joaoJID)

	linhas := func() []string { return strings.Split(b.Saude(ctx).Descrever(), "\n") }

	// Daemon de antes da transcrição: a linha do motor simplesmente não existe.
	l := linhas()
	if ultima := l[len(l)-1]; ultima != "áudios: 0 transcritos · 1 na fila (o mais antigo chegou há 2 horas)" {
		t.Errorf("linha de áudios = %q", ultima)
	}

	b.Anotar(ctx, "transcricao_motor", "whisper.cpp local · large-v3-turbo-q5_0 · nada sai desta máquina")
	l = linhas()
	if !strings.HasPrefix(l[0], "A PONTE NUNCA SUBIU") {
		t.Errorf("a primeira linha tem que continuar sendo o veredito, veio %q", l[0])
	}
	if ultima := l[len(l)-1]; ultima != "transcrição: whisper.cpp local · large-v3-turbo-q5_0 · nada sai desta máquina" {
		t.Errorf("linha da transcrição = %q", ultima)
	}

	b.Anotar(ctx, "transcricao_problema", `whisper.cpp: exec: "whisper-cli": executable file not found`)
	l = linhas()
	ultima := l[len(l)-1]
	if !strings.HasPrefix(ultima, "transcrição: PARADA — whisper.cpp: exec") || !strings.Contains(ultima, "whatsapp-reader verificar") {
		t.Errorf("com problema, a linha devia dizer PARADA e o que rodar: %q", ultima)
	}
}
