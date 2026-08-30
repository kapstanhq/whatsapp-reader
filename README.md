# whatsapp-reader

**Lê as suas conversas do WhatsApp e as entrega a um agente por MCP. Não envia mensagem.**

> *Read-only WhatsApp bridge exposing MCP tools. It never sends messages —
> sending is refused by design, not missing by accident. Written for a
> Portuguese-language plugin; docs below are in Portuguese.*

---

## Não procure aqui o que ele não faz

Se você chegou querendo **disparar mensagem, automatizar atendimento ou
responder cliente sozinho**, este não é o projeto — e não é falta de tempo de
implementar. Enviar em massa é o que faz uma conta de WhatsApp ser bloqueada, e
quem usa isto atende do número pessoal, onde estão os clientes dele.

O que sai daqui é texto para uma pessoa ler, copiar e enviar com o próprio dedo.

## Para que existe

Ele serve à [Oficina Kapstan](https://kapstan.com.br/oficina) — um pack de dez
skills para corretor de imóveis. Sem ele, o corretor cola a conversa na mão, e
tudo funciona igual. Com ele, o agente lê a conversa direto.

**O conector é opcional em todo o pack.** Nenhuma skill exige, e é o contrato
delas que garante isso.

## O que é, e o que não é

Não é fork de nada. São ~720 linhas que usam o
[whatsmeow](https://github.com/tulir/whatsmeow), a biblioteca que fala o
protocolo do WhatsApp:

```
banco.go     esquema SQLite e as consultas
servir.go    o daemon: conecta, pareia, captura histórico e mensagens
mcp.go       o protocolo MCP, JSON-RPC sobre stdio, escrito à mão
main.go      os dois subcomandos
```

O MCP é escrito à mão porque são quatro métodos — `initialize`, `ping`,
`tools/list`, `tools/call`. Um SDK custaria mais que o protocolo inteiro.

## Compilar

Precisa de Go. **Não precisa de compilador C**: o driver SQLite é
`modernc.org/sqlite`, em Go puro.

```sh
git clone https://github.com/kapstanhq/whatsapp-reader
cd whatsapp-reader
CGO_ENABLED=0 go build -o whatsapp-reader .
```

Compilação cruzada funciona de qualquer máquina para qualquer alvo:

```sh
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o whatsapp-reader-mac .
```

## Usar

```sh
whatsapp-reader serve     # janela própria: mantém a conexão e grava
whatsapp-reader mcp       # o que o agente executa: só lê o banco
```

**São dois processos, e a razão importa.** O `serve` precisa sobreviver ao
agente fechar: é ele que recebe as mensagens. Se a janela dele fechar, o
histórico congela no último momento em que ele esteve vivo, e **o que passou
enquanto ele esteve fora não volta**.

Na primeira execução, o `serve` mostra um QR — no terminal e também em
`qr.png`, para quando o terminal não desenha os blocos. Escaneie em
*Configurações › Dispositivos conectados › Conectar dispositivo*. O QR some do
disco assim que serve: é credencial.

Registrar no Claude Code:

```sh
claude mcp add whatsapp -- /caminho/para/whatsapp-reader mcp
```

## As tools

| tool | o que devolve |
|---|---|
| `listar_conversas` | conversas por atividade, com contagem; filtra por nome ou telefone |
| `listar_mensagens` | mensagens por conversa, por período ou por texto |
| `ultima_interacao` | quando foi a última mensagem, de quem, e há quantos dias |
| `estado_da_ponte` | quanto está guardado e até quando |

`ultima_interacao` existe porque uma das skills precisa saber há quantos dias
um cliente sumiu e de quem foi a última palavra. As tools servem as skills, e
não o contrário.

## Os dados

Ficam em `~/.kapstan/whatsapp-reader` (ou onde `WHATSAPP_READER_DIR` apontar):

```
mensagens.db   conversas e mensagens, em WAL
sessao.db      as chaves da sessão — é isto que mantém você conectado
```

**Nada sai da máquina.** Não há servidor, telemetria nem sincronização.

Tempo é gravado como inteiro, segundos desde a época, e não como `TIMESTAMP`:
`COALESCE(em, 0)` faz a coluna perder o tipo declarado e o driver recusa
converter para `time.Time`, e o formato de texto de quem grava não é
obrigatoriamente o de quem lê. Inteiro compara igual em qualquer caminho.

## Manutenção — leia antes de abrir issue

**A falha mais provável deste projeto tem sintoma mudo.** Quando o WhatsApp
atualizar o protocolo, a conexão passa a falhar assim:

```
websocket: close 1006 (abnormal closure): unexpected EOF
```

Isso não diz o que aconteceu, e o que aconteceu é que a versão do whatsmeow
envelheceu. O conserto:

```sh
go get -u go.mau.fi/whatsmeow@latest
go mod tidy
CGO_ENABLED=0 go build -o whatsapp-reader .
```

Se a compilação quebrar depois disso, é porque a API mudou — já aconteceu com
`Download`, `sqlstore.New`, `GetFirstDevice`, `GetGroupInfo` e `GetContact`,
que passaram a exigir `context.Context`. Costuma ser conserto mecânico.

O daemon também trata `ClientOutdatedEv`, que é o mesmo diagnóstico chegando
com nome em vez de código de erro.

## O que você precisa saber antes de usar

**Não é um programa oficial do WhatsApp.** Ele fala o mesmo protocolo do
WhatsApp Web, e a Meta não aprova esse tipo de acesso. Contas são bloqueadas
por disparo em massa — que é justamente o que este projeto recusa fazer. O
risco é baixo. Não é zero, e o número é seu.

Ele ocupa uma das quatro vagas de dispositivo conectado, e o WhatsApp pede o QR
de novo a cada ~20 dias.

E as conversas são de outras pessoas: ao guardá-las no seu disco, o
responsável por esses dados passa a ser você.

## Licença

MIT. O whatsmeow é MPL-2.0 e entra como dependência, sem modificação.
