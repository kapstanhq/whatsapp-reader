package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

/* MCP é JSON-RPC 2.0, uma mensagem por linha, em stdin/stdout. São quatro
   métodos, e por isso não há SDK aqui: a dependência custaria mais que o
   protocolo inteiro.

   REGRA DURA: stdout é do protocolo. Um Println solto no meio corrompe a
   sessão e o cliente desconecta sem dizer por quê — log vai para stderr. */

type reqRPC struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type errRPC struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type respRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *errRPC         `json:"error,omitempty"`
}

const versaoProtocolo = "2025-06-18"

func Mcp(dir string) error {
	// O `serve` cria a pasta; o `mcp` pode ser o primeiro a rodar — o corretor
	// registra a tool antes de parear. Sem isto o sqlite devolve
	// "unable to open database file (14)", que não diz o que falta.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("criar %s: %w", dir, err)
	}

	banco, err := AbrirBanco(filepath.Join(dir, "mensagens.db"))
	if err != nil {
		return fmt.Errorf("abrir banco: %w", err)
	}
	defer banco.Fechar()

	ctx := context.Background()
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)

	for {
		var r reqRPC
		err := dec.Decode(&r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "json malformado:", err)
			continue
		}
		// Sem id é notificação: o protocolo proíbe responder.
		if len(r.ID) == 0 {
			continue
		}
		res, erro := despachar(ctx, banco, dir, r)
		enc.Encode(respRPC{JSONRPC: "2.0", ID: r.ID, Result: res, Error: erro})
	}
}

func despachar(ctx context.Context, b *Banco, dir string, r reqRPC) (any, *errRPC) {
	switch r.Method {

	case "initialize":
		// Ecoar a versão que o cliente pediu evita brigar por compatibilidade
		// quando ele é mais velho que este binário.
		versao := versaoProtocolo
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(r.Params, &p) == nil && p.ProtocolVersion != "" {
			versao = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": versao,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "whatsapp-reader", "version": "0.1.0"},
		}, nil

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		return map[string]any{"tools": catalogo()}, nil

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil {
			return nil, &errRPC{Code: -32602, Message: "parâmetros inválidos"}
		}
		texto, err := executar(ctx, b, dir, p.Name, p.Arguments)
		if err != nil {
			// Falha de execução volta como resultado com isError, não como erro
			// de protocolo: o agente precisa LER o motivo para se corrigir.
			return map[string]any{
				"content": conteudo("erro: " + err.Error()),
				"isError": true,
			}, nil
		}
		return map[string]any{"content": conteudo(texto)}, nil
	}
	return nil, &errRPC{Code: -32601, Message: "método desconhecido: " + r.Method}
}

func conteudo(t string) []map[string]any {
	return []map[string]any{{"type": "text", "text": t}}
}

func catalogo() []map[string]any {
	str := func(d string) map[string]any {
		return map[string]any{"type": "string", "description": d}
	}
	num := func(d string) map[string]any {
		return map[string]any{"type": "integer", "description": d}
	}
	tool := func(nome, desc string, props map[string]any, obrig []string) map[string]any {
		if obrig == nil {
			obrig = []string{}
		}
		return map[string]any{
			"name":        nome,
			"description": desc,
			"inputSchema": map[string]any{
				"type": "object", "properties": props, "required": obrig,
			},
			// Vale para as quatro de leitura; as duas de envio sobrescrevem.
			"annotations": map[string]any{"readOnlyHint": true},
		}
	}
	anotar := func(t, a map[string]any) map[string]any {
		t["annotations"] = a
		return t
	}
	return []map[string]any{
		tool("listar_conversas",
			"Lista conversas, da mais recente para a mais antiga. Use busca para filtrar por nome ou telefone.",
			map[string]any{
				"busca":  str("parte do nome ou do telefone; vazio traz todas"),
				"limite": num("quantas conversas (padrão 20)"),
			}, nil),

		tool("listar_mensagens",
			"Lê mensagens. Filtre por conversa, por período ou por texto. Sem filtro, traz as mais recentes de todas.",
			map[string]any{
				"conversa": str("o jid da conversa, como veio de listar_conversas"),
				"busca":    str("texto a procurar dentro das mensagens"),
				"depois":   str("data mínima, AAAA-MM-DD ou ISO-8601"),
				"antes":    str("data máxima, AAAA-MM-DD ou ISO-8601"),
				"limite":   num("quantas mensagens (padrão 50)"),
			}, nil),

		tool("ultima_interacao",
			"Quando foi a última mensagem de uma conversa, de quem foi, e há quantos dias. Para saber quem está no silêncio.",
			map[string]any{"conversa": str("o jid da conversa")},
			[]string{"conversa"}),

		anotar(tool("preparar_envio",
			"Monta a PRÉVIA de uma mensagem: para quem vai e com que texto. NÃO envia nada — devolve um código. "+
				"MOSTRE a prévia inteira ao corretor (nome do destinatário e texto) e espere ELE dizer que pode. "+
				"Só então chame enviar_mensagem, repetindo código, conversa e texto.",
			map[string]any{
				"conversa": str("o jid de UMA conversa, como veio de listar_conversas. Uma só: não existe envio para lista"),
				"texto":    str("a mensagem exata, já escrita como o corretor a mandaria"),
			},
			[]string{"conversa", "texto"}),
			map[string]any{"readOnlyHint": false, "destructiveHint": false,
				"title": "Preparar mensagem (não envia)"}),

		anotar(tool("enviar_mensagem",
			"ENVIA a mensagem que preparar_envio mostrou. Exige o código da prévia e a repetição EXATA da conversa e do "+
				"texto — mudou qualquer coisa depois da prévia, é recusado. Uma conversa por chamada. "+
				"Chame só depois que o corretor tiver lido a prévia e autorizado.",
			map[string]any{
				"previa":   str("o código devolvido por preparar_envio"),
				"conversa": str("o mesmo jid da prévia"),
				"texto":    str("o mesmo texto da prévia, caractere por caractere"),
			},
			[]string{"previa", "conversa", "texto"}),
			map[string]any{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true,
				"title": "Enviar a mensagem no WhatsApp"}),

		tool("estado_da_ponte",
			"Quanto a ponte tem guardado e até quando. Use antes de afirmar que algo não existe.",
			map[string]any{}, nil),
	}
}

func executar(ctx context.Context, b *Banco, dir, nome string, args json.RawMessage) (string, error) {
	var a struct {
		Busca    string `json:"busca"`
		Conversa string `json:"conversa"`
		Depois   string `json:"depois"`
		Antes    string `json:"antes"`
		Limite   int    `json:"limite"`
		Texto    string `json:"texto"`
		Previa   string `json:"previa"`
	}
	if len(args) > 0 {
		json.Unmarshal(args, &a)
	}

	switch nome {

	case "listar_conversas":
		if a.Limite <= 0 {
			a.Limite = 20
		}
		cs, err := b.ListarConversas(ctx, a.Busca, a.Limite)
		if err != nil {
			return "", err
		}
		if len(cs) == 0 {
			return "nenhuma conversa. A ponte já sincronizou? Veja estado_da_ponte.", nil
		}
		var s strings.Builder
		for _, c := range cs {
			nome := c.Nome
			if nome == "" {
				nome = "(sem nome)"
			}
			fmt.Fprintf(&s, "%s · %s · %d mensagens · %s\n",
				c.Ultima.Format("2006-01-02 15:04"), nome, c.Total, c.JID)
		}
		return s.String(), nil

	case "listar_mensagens":
		if a.Limite <= 0 {
			a.Limite = 50
		}
		depois, err := parseData(a.Depois)
		if err != nil {
			return "", fmt.Errorf("depois: %w", err)
		}
		antes, err := parseData(a.Antes)
		if err != nil {
			return "", fmt.Errorf("antes: %w", err)
		}
		ms, err := b.ListarMensagens(ctx, a.Conversa, a.Busca, depois, antes, a.Limite)
		if err != nil {
			return "", err
		}
		if len(ms) == 0 {
			return "nenhuma mensagem com esses filtros.", nil
		}
		var s strings.Builder
		// do mais antigo para o mais novo: é a ordem em que se lê uma conversa
		for i := len(ms) - 1; i >= 0; i-- {
			m := ms[i]
			quem := m.Nome
			if m.DeMim {
				quem = "eu"
			} else if quem == "" {
				quem = m.Conversa
			}
			linha := m.Texto
			if m.Midia != "" {
				linha = "[" + m.Midia + "] " + linha
			}
			fmt.Fprintf(&s, "[%s] %s: %s\n", m.Em.Format("2006-01-02 15:04"), quem, linha)
		}
		return s.String(), nil

	case "ultima_interacao":
		em, deMim, texto, err := b.UltimaInteracao(ctx, a.Conversa)
		if err != nil {
			return "", err
		}
		if em.IsZero() {
			return "nenhuma mensagem nessa conversa.", nil
		}
		ultima := "a última palavra foi do cliente"
		if deMim {
			ultima = "a última palavra foi sua"
		}
		dias := int(time.Since(em).Hours() / 24)
		return fmt.Sprintf("última em %s · há %d dias · %s\ntexto: %s",
			em.Format("2006-01-02 15:04"), dias, ultima, texto), nil

	case "preparar_envio":
		elo, err := AbrirEloCliente(dir)
		if err != nil {
			return "", err
		}
		res, err := elo.pedir(ctx, "/preparar", map[string]string{
			"conversa": a.Conversa, "texto": a.Texto})
		if err != nil {
			return "", err
		}
		nome := campo(res, "nome")
		if nome == "" {
			nome = campo(res, "conversa")
		}
		// Este bloco é para ser MOSTRADO inteiro. Se o agente o resumir, quem
		// autoriza não viu o que autoriza — e é essa a única coisa que a
		// prévia serve para garantir.
		return fmt.Sprintf(
			"PRÉVIA %s · NADA FOI ENVIADO AINDA\n\npara: %s (%s)\ntexto:\n%s\n\n"+
				"Mostre isto ao corretor, com o nome e o texto inteiros, e espere ELE autorizar.\n"+
				"Depois: enviar_mensagem com previa=%s, a mesma conversa e o mesmo texto.\n"+
				"A prévia vale 10 minutos e serve uma vez só.",
			campo(res, "previa"), nome, campo(res, "conversa"), campo(res, "texto"),
			campo(res, "previa")), nil

	case "enviar_mensagem":
		elo, err := AbrirEloCliente(dir)
		if err != nil {
			return "", err
		}
		res, err := elo.pedir(ctx, "/enviar", map[string]string{
			"previa": a.Previa, "conversa": a.Conversa, "texto": a.Texto})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("enviada para %s (%s) · id %s",
			campo(res, "para"), campo(res, "conversa"), campo(res, "id")), nil

	case "estado_da_ponte":
		c, m := b.Contagem(ctx)
		periodo := "sem mensagens"
		ms, err := b.ListarMensagens(ctx, "", "", time.Time{}, time.Time{}, 1)
		if err == nil && len(ms) > 0 {
			periodo = "a mais recente é de " + ms[0].Em.Format("2006-01-02 15:04")
		}
		total, distintos, _, ultimo, _ := b.EnviosNaJanela(ctx, time.Now().Add(-time.Hour), "")
		envios := fmt.Sprintf("nesta hora saíram %d mensagens pela ponte, para %d conversas (teto: %d)",
			total, distintos, distintosHora)
		if !ultimo.IsZero() {
			envios += " · a última às " + ultimo.Format("15:04")
		}
		return fmt.Sprintf("%d conversas, %d mensagens · %s\n%s", c, m, periodo, envios), nil
	}
	return "", fmt.Errorf("tool desconhecida: %s", nome)
}

func parseData(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	formatos := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
	for _, f := range formatos {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("data não reconhecida: %q (use AAAA-MM-DD)", s)
}

// O elo devolve JSON solto. Isto evita um %v imprimindo <nil> no meio do texto
// que o corretor vai ler.
func campo(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
