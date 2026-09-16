package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

/* A SAÚDE DA PONTE, e por que ela mora no banco.

   O defeito que isto conserta foi medido: o daemon morreu num 01/09 às 11:05 e
   `estado_da_ponte` seguiu respondendo "205 conversas, 3143 mensagens · a mais
   recente é de 2026-09-01" por SEIS DIAS, sem uma palavra sobre estar fora do
   ar. Ele lia só o banco, e o banco de um daemon morto parece o banco de um
   daemon parado — a diferença não estava escrita em lugar nenhum.

   O `elo.json` não serve para responder isso. Ele some no `Fechar()` gracioso,
   mas fica para trás quando o processo morre de vez (reboot, kill, janela
   fechada no X), apontando para uma porta que já não é de ninguém. E o `mcp`
   que só olha o arquivo não sabe distinguir "vivo" de "morreu sem se despedir".

   Então o daemon BATE: grava a hora no banco a cada 30 s. O `mcp` já abre esse
   banco para tudo o mais, e a batida velha é a única prova barata de que o
   outro processo não está lá. Vale mesmo com o daemon morto — que é justamente
   quando a pergunta é feita. */

const (
	batidaIntervalo  = 30 * time.Second
	batidaTolerancia = 90 * time.Second // três batidas perdidas: o processo se foi
)

const esquemaEstado = `
CREATE TABLE IF NOT EXISTS estado (
  chave TEXT PRIMARY KEY,
  valor TEXT,
  em    INTEGER NOT NULL
);
`

func (b *Banco) Anotar(ctx context.Context, chave, valor string) error {
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO estado (chave, valor, em) VALUES (?, ?, ?)
		ON CONFLICT(chave) DO UPDATE SET valor = excluded.valor, em = excluded.em`,
		chave, valor, time.Now().Unix())
	return err
}

func (b *Banco) LerEstado(ctx context.Context, chave string) (string, time.Time) {
	var valor string
	var em int64
	err := b.db.QueryRowContext(ctx,
		`SELECT COALESCE(valor,''), em FROM estado WHERE chave = ?`, chave).Scan(&valor, &em)
	if err != nil || em == 0 {
		return "", time.Time{}
	}
	return valor, time.Unix(em, 0)
}

type Saude struct {
	Vivo      bool      // o daemon bateu há menos de batidaTolerancia
	Batida    time.Time // quando ele bateu pela última vez
	Conectado bool      // e, estando vivo, se o WhatsApp respondia
	Motivo    string    // a última coisa que aconteceu com a conexão
	Numero    string    // o número do próprio corretor, para o envio de teste
	UltimaMsg time.Time // a mensagem mais nova que existe no banco
	Conversas int
	Mensagens int
	Audios    ResumoAudios // o que a esteira de mídia tem, e o que falta
	// O motor de transcrição e o que o impede, como o daemon anotou: o `mcp`
	// não enxerga o ambiente da janela do `serve`.
	Transcricao, TranscricaoProblema string
}

func (b *Banco) Saude(ctx context.Context) Saude {
	var s Saude
	_, s.Batida = b.LerEstado(ctx, "batida")
	s.Vivo = !s.Batida.IsZero() && time.Since(s.Batida) < batidaTolerancia
	conectado, _ := b.LerEstado(ctx, "conectado")
	s.Conectado = s.Vivo && conectado == "1"
	s.Motivo, _ = b.LerEstado(ctx, "motivo")
	s.Numero, _ = b.LerEstado(ctx, "numero")
	s.Conversas, s.Mensagens = b.Contagem(ctx)
	if ms, err := b.ListarMensagens(ctx, "", "", time.Time{}, time.Time{}, 1); err == nil && len(ms) > 0 {
		s.UltimaMsg = ms[0].Em
	}
	s.Audios = b.ResumoAudios(ctx)
	s.Transcricao, _ = b.LerEstado(ctx, "transcricao_motor")
	s.TranscricaoProblema, _ = b.LerEstado(ctx, "transcricao_problema")
	return s
}

/* O texto que o corretor lê, e a primeira linha é sempre o veredito.

   Ela vem primeiro de propósito: quem pergunta o estado da ponte quer saber se
   pode confiar no que vier depois, e um número de conversas no topo é lido como
   "está tudo bem" antes de a segunda linha ser lida. */

func (s Saude) Descrever() string {
	var cabeca string
	switch {
	case s.Vivo && s.Conectado:
		cabeca = "A ponte está de pé e conectada."
	case s.Vivo && !s.Conectado:
		cabeca = "O daemon está rodando, MAS NÃO ESTÁ CONECTADO ao WhatsApp — " +
			"nada novo está entrando."
		if s.Motivo != "" {
			cabeca += "\nO que aconteceu: " + s.Motivo
		}
	case s.Batida.IsZero():
		cabeca = "A PONTE NUNCA SUBIU nesta máquina, ou subiu numa versão antiga " +
			"que ainda não anotava a batida.\nAbra uma janela e rode `whatsapp-reader serve`."
	default:
		cabeca = fmt.Sprintf("A PONTE ESTÁ FORA DO AR há %s (a última batida do daemon "+
			"foi em %s).\nNada que chegou depois disso foi gravado, e o WhatsApp NÃO reenvia "+
			"o que passou.\nAbra uma janela e rode `whatsapp-reader serve`.",
			humano(time.Since(s.Batida)), s.Batida.Format("02/01 15:04"))
		if s.Motivo != "" {
			cabeca += "\nA última coisa que ela disse: " + s.Motivo
		}
	}

	periodo := "sem mensagens"
	if !s.UltimaMsg.IsZero() {
		periodo = fmt.Sprintf("a mais recente é de %s", s.UltimaMsg.Format("2006-01-02 15:04"))
		// Histórico velho com o daemon VIVO é outra história: pode ser só um
		// dia parado. O alarme é a combinação, e ela já está na primeira linha.
		if d := time.Since(s.UltimaMsg); d > 48*time.Hour {
			periodo += fmt.Sprintf(" — há %s", humano(d))
		}
	}
	out := fmt.Sprintf("%s\n\n%d conversas, %d mensagens · %s",
		cabeca, s.Conversas, s.Mensagens, periodo)
	// A linha dos áudios só aparece quando há áudio: numa ponte antiga ou recém
	// pareada ela seria um zero que ninguém pediu.
	if s.Audios.Total > 0 {
		out += "\n" + s.Audios.Linha()
		if l := s.linhaTranscricao(); l != "" {
			out += "\n" + l
		}
	}
	return out
}

// O problema vem antes do motor: "whisper.cpp local" com a fila parada é a
// informação errada no lugar de destaque.
func (s Saude) linhaTranscricao() string {
	switch {
	case s.TranscricaoProblema != "":
		return "transcrição: PARADA — " + s.TranscricaoProblema +
			". Rode `whatsapp-reader verificar` na máquina da ponte"
	case s.Transcricao != "":
		return "transcrição: " + s.Transcricao
	}
	return "" // daemon de antes da transcrição, que não anotava o motor
}

func humano(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0f segundos", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%.0f minutos", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.0f horas", d.Hours())
	}
	return fmt.Sprintf("%d dias", int(d.Hours()/24))
}

/* A batida, do lado do daemon. Ela grava o pid junto porque o `elo.json`
   também o grava: quando os dois discordam, há um segundo `serve` no ar — e
   dois clientes sobre o mesmo `sessao.db` corrompem o ratchet do Signal. */

func baterSempre(ctx context.Context, b *Banco) {
	pid := fmt.Sprint(os.Getpid())
	b.Anotar(ctx, "batida", pid)
	t := time.NewTicker(batidaIntervalo)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.Anotar(ctx, "batida", pid)
		}
	}
}

/* UMA ponte por máquina, e a batida é quem decide.

   O pid já era gravado, e o comentário acima já dizia o preço de dois `serve`
   no ar — mas ninguém comparava, e subir o segundo era um comando. Com o
   daemon no agendador do sistema (o degrau 4.5 da instalação) isso deixa de
   ser descuido raro: quem reinicia a máquina tem um `serve` de pé sem janela
   nenhuma aberta, e abrir uma e rodar o comando é o gesto natural.

   A regra é a batida, não o pid: pid se recicla depois de um reboot, e um
   número igual por acaso liberaria justamente o caso que isto barra. Quem
   morreu de vez fica até 90 s sem poder voltar — é o preço, e a mensagem o
   diz em vez de deixar a pessoa adivinhando. */

func (b *Banco) OutroDaemon(ctx context.Context) error {
	pid, em := b.LerEstado(ctx, "batida")
	if em.IsZero() || time.Since(em) >= batidaTolerancia {
		return nil
	}
	return fmt.Errorf("já há uma ponte de pé nesta máquina: o processo %s bateu há %s.\n"+
		"Dois `serve` sobre a mesma sessão corrompem o ratchet do Signal, e as mensagens "+
		"passam a chegar sem decifrar.\nUse a janela que já está aberta — `whatsapp-reader "+
		"estado` diz o que ela está fazendo.\nSe aquele processo acabou de morrer, espere "+
		"um minuto e rode de novo.", pid, humano(time.Since(em)))
}

/* O subcomando `estado`: o mesmo diagnóstico sem MCP e sem agente.

   Existe porque a cadeia de instalação precisa de um degrau que se CONFIRME
   antes de o programa ter as tools registradas — e porque quem for pôr o daemon
   numa Tarefa Agendada precisa de um jeito de perguntar se ele subiu. */

func Estado(dir string) error {
	banco, err := AbrirBanco(dirBanco(dir))
	if err != nil {
		return fmt.Errorf("abrir banco: %w", err)
	}
	defer banco.Fechar()
	ctx := context.Background()
	s := banco.Saude(ctx)
	fmt.Println(s.Descrever())
	if s.Numero != "" {
		fmt.Printf("\no seu número nesta ponte: %s\n", s.Numero)
	}
	if !s.Vivo {
		os.Exit(1) // para quem chamar isto de um script
	}
	return nil
}

// Só para o Estado() não repetir o filepath.Join que o Servir e o Mcp fazem.
func dirBanco(dir string) string { return filepath.Join(dir, "mensagens.db") }
