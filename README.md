# ControlFinan (Go)

![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Tailwind](https://img.shields.io/badge/Tailwind-4-06B6D4?logo=tailwindcss&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)

Aplicação web de controle financeiro pessoal, pensada para quem trabalha como motorista de aplicativo. Cada usuário registra entradas (créditos) e saídas (débitos) em contas como dinheiro, bancos e carteiras de aplicativos, e acompanha o saldo por dia e por mês.

Esta é a versão em **Go** do [ControlFinan em Rails 8 + Hotwire](https://github.com/igorcb/controlfinan-rails8-hotwire). Ela tem **paridade de funcionalidades e de visual** com o original, assim como as versões em [Django](https://github.com/igorcb/controlfinan-django) e em [Laravel](https://github.com/igorcb/controlfinan-laravel): mesmas telas, mesmas regras, mesmas mensagens e **mesmo esquema de banco**. As versões podem usar os mesmos dados, inclusive as senhas.

Usa só a biblioteca padrão do Go (`net/http` e `html/template`), mais o driver [pgx](https://github.com/jackc/pgx) e o `bcrypt` de `golang.org/x/crypto`.

## Funcionalidades

| Tela | Rota | O que faz |
|---|---|---|
| Conta Corrente | `/` | Lista os lançamentos, do mais recente para o mais antigo, 30 por página, com o saldo total |
| Saldo do dia | `/current_accounts?date=AAAA-MM-DD` | Filtra a listagem por um dia e mostra o saldo daquele dia |
| Novo lançamento | `/current_accounts/new` | Cadastra um crédito ou um débito |
| Detalhe, edição e exclusão | `/current_accounts/:id` | Mostra, altera ou remove um lançamento |
| Dashboard | `/dashboard` | Abas **Saldo Diário** e **Saldo Mensal** (corridas menos combustível, lavagem e telefone). As setas navegam entre os meses |
| JSON | `/current_accounts.json` e `/current_accounts/:id.json` | Os mesmos dados, para integrações |
| Cadastro, login e senha | `/registration/new`, `/session/new`, `/passwords/new` | Conta de usuário, com senha de no mínimo 6 caracteres |

Cada usuário só enxerga os próprios lançamentos.

## Como a paridade foi feita

| Rails 8 + Hotwire | Go |
|---|---|
| Autenticação nativa do Rails 8 (`has_secure_password`, tabela `sessions`, cookie `session_id`) | Mesmo modelo: tabela `sessions` e cookie assinado `session_id`. O `bcrypt` do Go grava no mesmo formato do Rails (`$2a$`) |
| Proteção CSRF (`authenticity_token`) | Token em cookie repetido em cada formulário, com o mesmo nome de campo |
| `rate_limit to: 10, within: 3.minutes` | Limite de 10 tentativas a cada 3 minutos por IP, em memória |
| Token de redefinição de senha (15 minutos) | Token assinado com validade de 15 minutos, invalidado quando a senha muda |
| `_method` para PATCH, PUT e DELETE | O mesmo campo `_method` nos formulários |
| Turbo (`data-turbo-confirm`) e Stimulus (abas) | JavaScript simples para a confirmação e as abas |
| jbuilder | JSON compacto com os mesmos campos e o mesmo formato (por exemplo, `"price":"150.0"`) |
| Tailwind CSS 4 | Tailwind CSS 4 (CLI standalone), com as mesmas classes |

Os valores em dinheiro ficam em centavos (`int64`), sem ponto flutuante.

**Verificação**, com o mesmo usuário e os mesmos dados (7.292 lançamentos) nos dois apps:

- Texto idêntico em todas as telas comparadas, incluindo paginação, filtro por dia, datas e meses inválidos, dashboard, formulários, mensagens de validação, erros de cadastro e login inválido.
- JSON idêntico byte a byte (lista, segunda página e detalhe).
- **14 telas idênticas pixel a pixel**: login, lista, filtro, dashboard, novo, detalhe e edição, no computador (1280 px) e no celular (390 px).
- Roteiro completo no navegador (Playwright): cadastro, login, lançamentos, edição, exclusão, dashboard, recuperação de senha e celular.

## Benchmark

O benchmark que compara as nove versões do ControlFinan (Rails, Django, Laravel, Go, Java, Rust, C#, Node.js e Elixir), com os mesmos dados e os mesmos limites de CPU e memória, fica no repositório do app de referência: [controlfinan-rails8-hotwire/benchmark](https://github.com/igorcb/controlfinan-rails8-hotwire/tree/main/benchmark).

## Como rodar com Docker

```bash
docker compose up               # app em http://localhost:8003
```

O container de desenvolvimento compila o Tailwind e roda o app com `go run .`. As tabelas são criadas na subida (mesmo esquema do app Rails). Depois de mudar código Go, reinicie com `docker compose restart web`.

Imagem de produção: `Dockerfile.prod`, um binário estático (templates, CSS, JS e fuso horário embutidos) numa imagem vazia, com cerca de 13 MB. Configure `DATABASE_URL`, `SECRET_KEY_BASE` e, se quiser, `BCRYPT_COST` (padrão 12), `DB_POOL` (padrão 10) e `PORT` (padrão 8000).

## Testes

36 testes com o pacote `testing` do Go, usando Postgres de verdade, cobrindo:

- Formatação de moeda e decimais.
- Senha no formato do Rails, incluindo um hash gravado pelo próprio Rails.
- Validações em pt-BR, saldo e resumo do mês.
- Login, limite de tentativas, logout, CSRF, cadastro e redefinição de senha.
- Lançamentos: filtro, paginação, JSON, criar, editar, excluir e isolamento entre usuários.
- Dashboard.

```bash
docker compose exec web go test ./...
```

Os testes usam o banco `controlfinan_go_test`, criado automaticamente.

## Estrutura

```
main.go                 configuração (variáveis de ambiente) e servidor HTTP
app/server.go           rotas e handlers
app/web.go              cookies assinados, sessão, CSRF, flash e limite de tentativas
app/models.go           usuários, sessões, lançamentos, validações e consultas
app/format.go           moeda e decimais como no Rails
app/templates/          HTML com o mesmo resultado do app Rails
app/static/             JavaScript e ícones (o CSS é gerado pelo Tailwind)
assets/                 fonte do Tailwind
```

## Licença

[MIT](LICENSE)
