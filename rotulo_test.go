package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLinhaDaMensagem(t *testing.T) {
	audio := func(a AudioVisto) MensagemVista { return MensagemVista{Midia: "áudio", Audio: a} }
	baixado := func(transcricao, texto, erro string) MensagemVista {
		return audio(AudioVisto{Estado: midiaBaixada, Segundos: 42, Transcricao: transcricao, Texto: texto, Erro: erro})
	}
	parada := "a transcrição está parada, veja estado_da_ponte"

	casos := []struct {
		nome   string
		m      MensagemVista
		parada string
		quer   string
	}{
		{"texto", MensagemVista{Texto: "oi"}, "", "oi"},
		{"imagem com legenda", MensagemVista{Midia: "imagem", Texto: "a fachada"}, "", "[imagem] a fachada"},
		{"áudio de antes da esteira", audio(AudioVisto{}), "", "[áudio] "},
		{"transcrito", baixado("feita", "olha, sobre as calhas", ""), "", "[áudio 0:42 · transcrição] olha, sobre as calhas"},
		{"silêncio", baixado("feita", "", ""), "", "[áudio 0:42 · sem fala reconhecível]"},
		{"na fila", baixado("pendente", "", ""), "", "[áudio 0:42 · na fila para transcrever]"},
		{"na fila com a transcrição parada", baixado("pendente", "", ""), parada,
			"[áudio 0:42 · na fila para transcrever — a transcrição está parada, veja estado_da_ponte]"},
		{"transcrevendo", baixado("transcrevendo", "", ""), parada, "[áudio 0:42 · transcrevendo agora]"},
		{"transcrição falhou", baixado("falhou", "", "whisper.cpp: terminou sem escrever\n   a transcrição"), "",
			"[áudio 0:42 · a transcrição falhou: whisper.cpp: terminou sem escrever a transcrição]"},
		{"esperando o download", audio(AudioVisto{Estado: midiaPendente, Segundos: 65}), parada, "[áudio 1:05 · na fila para baixar]"},
		{"baixando", audio(AudioVisto{Estado: midiaBaixando, Segundos: 42}), "", "[áudio 0:42 · baixando agora]"},
		{"pedido ao celular", audio(AudioVisto{Estado: midiaPedida, Segundos: 42}), "", "[áudio 0:42 · pedido ao celular, esperando]"},
		{"expirou", audio(AudioVisto{Estado: midiaIndisponivel, Segundos: 42}), "", "[áudio 0:42 · o arquivo expirou e o celular não tem mais]"},
		{"grupo", audio(AudioVisto{Estado: midiaGuardada, Motivo: "grupo", Segundos: 42}), "", "[áudio 0:42 · não baixado: grupo]"},
		{"visualização única, sem duração", audio(AudioVisto{Estado: midiaIgnorada, Motivo: "visualização única"}), "",
			"[áudio · não baixado: visualização única]"},
		{"guardado sem motivo", audio(AudioVisto{Estado: midiaGuardada, Segundos: 42}), "", "[áudio 0:42 · não baixado]"},
		{"download falhou com erro longo", audio(AudioVisto{Estado: midiaFalhou, Segundos: 42, Motivo: strings.Repeat("x", 100)}), "",
			"[áudio 0:42 · o download falhou: " + strings.Repeat("x", 80) + "…]"},
	}
	for _, c := range casos {
		if got := linhaDaMensagem(c.m, c.parada); got != c.quer {
			t.Errorf("%s:\n veio %q\n era  %q", c.nome, got, c.quer)
		}
	}
}

// O caminho inteiro do MCP: a mensagem de áudio no banco, a transcrição ao
// lado, e o texto que o agente recebe de listar_mensagens e ultima_interacao.
func TestMCPLeEBuscaATranscricao(t *testing.T) {
	ctx := context.Background()
	b := bancoTeste(t)
	em := time.Date(2026, 9, 15, 14, 0, 0, 0, time.Local)
	b.GravarConversa(ctx, joaoJID, "seu João", em)
	b.GravarMensagem(ctx, Mensagem{ID: "T1", Conversa: joaoJID, Texto: "tudo certo com a obra?", DeMim: true, Em: em})
	b.GravarMensagem(ctx, Mensagem{ID: "A1", Conversa: joaoJID, Midia: "áudio", Em: em.Add(time.Minute)})
	b.db.Exec(`INSERT INTO midias (mensagem, conversa, tipo, segundos, em, estado, arquivo)
	           VALUES ('A1', ?, 'audio', 42, ?, 'baixada', 'midia/ab/ab.ogg')`, joaoJID, em.Unix())
	b.db.Exec(`INSERT INTO transcricoes (mensagem, conversa, estado, texto)
	           VALUES ('A1', ?, 'feita', 'olha, amanhã eu passo aí para ver as calhas')`, joaoJID)

	chamar := func(tool, args string) string {
		t.Helper()
		out, err := executar(ctx, b, t.TempDir(), tool, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return out
	}

	achou := chamar("listar_mensagens", `{"busca": "calhas"}`)
	if quer := "seu João: [áudio 0:42 · transcrição] olha, amanhã eu passo aí para ver as calhas"; !strings.Contains(achou, quer) {
		t.Errorf("a busca devia achar a palavra dita no áudio:\n%s", achou)
	}
	if strings.Contains(achou, "tudo certo") {
		t.Errorf("a busca trouxe mensagem que não tem a palavra:\n%s", achou)
	}
	if escrita := chamar("listar_mensagens", `{"busca": "obra"}`); !strings.Contains(escrita, "eu: tudo certo com a obra?") {
		t.Errorf("a busca no texto escrito continua valendo:\n%s", escrita)
	}

	ultima := chamar("ultima_interacao", `{"conversa": "`+joaoJID+`"}`)
	if !strings.Contains(ultima, "texto: [áudio 0:42 · transcrição] olha, amanhã") || !strings.Contains(ultima, "do cliente") {
		t.Errorf("a última interação devia trazer o que foi dito no áudio:\n%s", ultima)
	}
	if vazia := chamar("ultima_interacao", `{"conversa": "5551000000000@s.whatsapp.net"}`); vazia != "nenhuma mensagem nessa conversa." {
		t.Errorf("conversa sem mensagem = %q", vazia)
	}

	// Pendente com o motor quebrado: o rótulo aponta para o estado da ponte.
	b.db.Exec(`UPDATE transcricoes SET estado = 'pendente', texto = NULL WHERE mensagem = 'A1'`)
	b.Anotar(ctx, "transcricao_problema", `whisper.cpp: exec: "whisper-cli": executable file not found`)
	if parada := chamar("listar_mensagens", `{"conversa": "`+joaoJID+`"}`); !strings.Contains(parada, "na fila para transcrever — a transcrição está parada") {
		t.Errorf("com o motor parado o rótulo devia dizer:\n%s", parada)
	}
}

func TestTranscricaoParada(t *testing.T) {
	ctx := context.Background()
	b := bancoTeste(t)
	if p := b.transcricaoParada(ctx); p != "" {
		t.Errorf("sem nada anotado (daemon antigo) = %q", p)
	}
	b.Anotar(ctx, "transcricao_motor", "whisper.cpp local · small-q5_1 · nada sai desta máquina")
	if p := b.transcricaoParada(ctx); p != "" {
		t.Errorf("motor de pé = %q", p)
	}
	b.Anotar(ctx, "transcricao_motor", "DESLIGADA por WHATSAPP_READER_TRANSCRICAO=desligada")
	if p := b.transcricaoParada(ctx); !strings.Contains(p, "desligada") {
		t.Errorf("desligada = %q", p)
	}
	b.Anotar(ctx, "transcricao_problema", "falta o ffmpeg")
	if p := b.transcricaoParada(ctx); !strings.Contains(p, "parada") {
		t.Errorf("com problema = %q", p)
	}
}
