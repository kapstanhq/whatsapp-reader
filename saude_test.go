package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSegundaPonteNaoSobe(t *testing.T) {
	ctx := context.Background()
	b := bancoTeste(t)

	// Banco sem batida nenhuma: é a primeira vez, e ela sobe.
	if err := b.OutroDaemon(ctx); err != nil {
		t.Fatalf("sem batida no banco, o daemon tem que subir: %v", err)
	}

	b.Anotar(ctx, "batida", "4242")
	err := b.OutroDaemon(ctx)
	if err == nil {
		t.Fatal("com batida de agora, o segundo serve tem que recusar")
	}
	// O pid vai na mensagem para a pessoa achar a janela; sem ele o recado é
	// "está rodando em algum lugar", que não ajuda ninguém.
	if !strings.Contains(err.Error(), "4242") {
		t.Errorf("a recusa tem que nomear o processo: %q", err)
	}

	// Batida velha é daemon morto, e o lugar não fica reservado para sempre.
	b.db.Exec(`UPDATE estado SET em = ? WHERE chave = 'batida'`,
		time.Now().Add(-batidaTolerancia-time.Second).Unix())
	if err := b.OutroDaemon(ctx); err != nil {
		t.Errorf("passada a tolerância, o daemon novo tem que subir: %v", err)
	}
}

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
