package main

import (
	"context"
	"fmt"
	"strings"
)

/* O RÓTULO de uma mensagem com áudio, que é o que o agente lê.

   Ele diz numa linha o que se sabe e por que não se sabe mais. "[áudio]"
   sozinho não distingue "ainda vai virar texto" de "nunca vai", e o agente
   responde ao corretor como se o áudio não dissesse nada. A transcrição vem
   marcada como transcrição porque é de máquina — troca nome, número e valor —
   e quem lê precisa saber de onde veio o texto antes de agir sobre ele. */

func linhaDaMensagem(m MensagemVista, parada string) string {
	a := m.Audio
	if a.Estado == "" {
		if m.Midia == "" {
			return m.Texto
		}
		return "[" + m.Midia + "] " + m.Texto
	}

	cabeca := "áudio"
	if a.Segundos > 0 {
		cabeca += fmt.Sprintf(" %d:%02d", a.Segundos/60, a.Segundos%60)
	}
	var situacao string
	switch a.Estado {
	case midiaGuardada, midiaIgnorada:
		situacao = strings.TrimSuffix("não baixado: "+a.Motivo, ": ")
	case midiaPendente:
		situacao = "na fila para baixar"
	case midiaBaixando:
		situacao = "baixando agora"
	case midiaPedida:
		situacao = "pedido ao celular, esperando"
	case midiaIndisponivel:
		situacao = "o arquivo expirou e o celular não tem mais"
	case midiaFalhou:
		situacao = "o download falhou: " + curto(a.Motivo)
	case midiaBaixada:
		switch a.Transcricao {
		case "feita":
			if a.Texto != "" {
				return juntar("["+cabeca+" · transcrição] "+a.Texto, m.Texto)
			}
			situacao = "sem fala reconhecível"
		case "transcrevendo":
			situacao = "transcrevendo agora"
		case "falhou":
			situacao = "a transcrição falhou: " + curto(a.Erro)
		default:
			situacao = "na fila para transcrever"
			if parada != "" {
				situacao += " — " + parada
			}
		}
	default:
		situacao = a.Estado
	}
	return juntar("["+cabeca+" · "+situacao+"]", m.Texto)
}

// Áudio quase nunca tem texto próprio, mas se um dia tiver, ele não some.
func juntar(rotulo, texto string) string {
	if texto == "" {
		return rotulo
	}
	return rotulo + " " + texto
}

// O erro cabe no rótulo; o inteiro está no estado da ponte e no log do daemon.
func curto(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80]) + "…"
	}
	if s == "" {
		return "sem detalhe"
	}
	return s
}

// Por que a fila de transcrição não anda, curto o bastante para caber no
// rótulo de cada áudio pendente. Vazio quando anda.
func (b *Banco) transcricaoParada(ctx context.Context) string {
	if p, _ := b.LerEstado(ctx, "transcricao_problema"); p != "" {
		return "a transcrição está parada, veja estado_da_ponte"
	}
	if motor, _ := b.LerEstado(ctx, "transcricao_motor"); strings.HasPrefix(motor, "DESLIGADA") {
		return "a transcrição está desligada nesta ponte"
	}
	return ""
}
