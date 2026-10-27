"""Turns the k6 summaries and docker stats samples into a Markdown report. Usage: python3 report.py results/"""

import json
import re
import sys
from pathlib import Path

RESULTS = Path(sys.argv[1] if len(sys.argv) > 1 else "results")
ROOT = Path(__file__).resolve().parent.parent
APPS = {
    "rails": "Rails 8.1 + Hotwire (Puma)",
    "django": "Django 5.2 (Gunicorn)",
    "laravel": "Laravel 12 + Octane (FrankenPHP)",
    "go": "Go 1.25 (net/http + pgx)",
}
NAMES = {"rails": "Rails", "django": "Django", "laravel": "Laravel", "go": "Go"}
PAGES = {
    "lista": "Conta Corrente (30 itens + saldo)",
    "dia": "Saldo do dia (filtro)",
    "dashboard": "Dashboard do mês",
    "json": "API JSON (30 itens)",
    "criar": "Criar lançamento (POST)",
}
# Code that implements the app in each repo (blank lines ignored, generated files and migrations excluded).
CODE = {
    "rails": (ROOT.parent / "controlfinan-rails8-hotwire", [
        "app/**/*.rb", "app/**/*.erb", "app/**/*.jbuilder", "app/javascript/**/*.js",
        "app/assets/stylesheets/*.css", "config/routes.rb",
    ], ["app/assets/builds"]),
    "django": (ROOT.parent / "controlfinan-django", [
        "finance/**/*.py", "finance/**/*.html", "finance/**/*.txt", "finance/static/finance/js/*.js",
        "finance/assets/app.tailwind.css", "config/urls.py",
    ], ["migrations"]),
    "laravel": (ROOT.parent / "controlfinan-laravel", [
        "app/**/*.php", "resources/views/**/*.blade.php", "public/js/*.js", "resources/css/*.css", "routes/web.php",
    ], []),
    "go": (ROOT, ["main.go", "app/*.go", "app/templates/*.html", "app/static/js/*.js", "assets/*.css"], ["_test.go"]),
}
TESTS = {
    "rails": (ROOT.parent / "controlfinan-rails8-hotwire", ["test/**/*.rb", "spec/**/*.rb"]),
    "django": (ROOT.parent / "controlfinan-django", ["tests/*.py"]),
    "laravel": (ROOT.parent / "controlfinan-laravel", ["tests/**/*.php"]),
    "go": (ROOT, ["app/*_test.go"]),
}


def mib(text):
    value, unit = re.match(r"([\d.]+)\s*([KMG]?i?B)", text.strip()).groups()
    factors = {"B": 1 / 1024 / 1024, "KiB": 1 / 1024, "MiB": 1, "GiB": 1024, "kB": 1 / 1000, "MB": 1, "GB": 1000}
    return float(value) * factors[unit]


def k6(app, page):
    data = json.loads((RESULTS / f"{app}-{page}.json").read_text())["metrics"]
    duration = data["http_req_duration{phase:load}"]["values"]
    return {
        "rps": data["http_reqs{phase:load}"]["values"]["count"] / 30,
        "p50": duration["p(50)"],
        "p95": duration["p(95)"],
        "p99": duration["p(99)"],
        "errors": data["http_req_failed{phase:load}"]["values"]["rate"] * 100,
    }


def stats(app, page, kind="stats"):
    path = RESULTS / f"{app}-{page}-{kind}.txt"
    if not path.exists():
        return {"cpu_avg": float("nan"), "mem_peak": float("nan")}
    lines = [line.split(";") for line in path.read_text().splitlines() if ";" in line]
    # Ignore samples taken while the container was stopping (0B of memory).
    lines = [(cpu, mem) for cpu, mem in lines if mib(mem.split("/")[0]) > 0]
    cpu = [float(c.strip("%")) for c, _ in lines]
    mem = [mib(m.split("/")[0]) for _, m in lines]
    return {"cpu_avg": sum(cpu) / len(cpu), "mem_peak": max(mem)}


def lines_of_code(root, patterns, exclude=()):
    total = 0
    for pattern in patterns:
        for path in root.glob(pattern):
            if path.is_file() and not any(part in str(path) for part in exclude):
                total += sum(1 for line in path.read_text(errors="ignore").splitlines() if line.strip())
    return total


def ranking(values, higher_is_better):
    """'Rails (1.4x Django, 2.0x Laravel)': the best app and how far ahead of each other app it is."""
    order = sorted(values, key=values.get, reverse=higher_is_better)
    best = order[0]
    ratios = []
    for other in order[1:]:
        ratio = values[best] / values[other] if higher_is_better else values[other] / values[best]
        ratios.append(f"{ratio:.1f}x {NAMES[other]}")
    return f"{NAMES[best]} ({', '.join(ratios)})", order


def main():
    apps = [app for app in APPS if (RESULTS / f"{app}-meta.json").exists()]
    out = ["# Benchmark: ControlFinan em " + " x ".join(NAMES[a] for a in apps), ""]
    out += [
        "Mesmo app (paridade de telas e regras), mesmos dados, modo produção.",
        "Puma e Gunicorn: 2 processos × 5 threads (com GVL/GIL, até 2 requisições usam CPU ao mesmo tempo); Laravel Octane: 2 workers, um por CPU; Go: goroutines em 2 threads (GOMAXPROCS = limite de CPU).",
        "Cada app limitado a 2 CPUs e 1 GB de RAM, com o seu Postgres 16 (2 CPUs, 1 GB).",
        "Carga: k6 com 20 usuários simultâneos, 10 s de aquecimento + 30 s medidos por tela, logado de verdade.",
        "Os dados são carregados e analisados (`ANALYZE`) antes da medição.",
        "",
        "Versões: " + "; ".join(APPS[a] for a in apps) + ".",
        "",
        "## Requisições por segundo e latência",
        "",
        "| Tela | App | req/s | p50 (ms) | p95 (ms) | p99 (ms) | erros | CPU média | memória pico | CPU do banco |",
        "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    summary, wins = {}, {app: 0 for app in apps}
    for page, label in PAGES.items():
        rps = {}
        for app in apps:
            r, s, d = k6(app, page), stats(app, page), stats(app, page, "dbstats")
            rps[app] = r["rps"]
            flag = " ⚠️ inválido" if r["errors"] > 1 else ""
            out.append(
                f"| {label} | {NAMES[app]}{flag} | {r['rps']:.0f} | {r['p50']:.1f} | {r['p95']:.1f} | {r['p99']:.1f} | "
                f"{r['errors']:.1f}% | {s['cpu_avg']:.0f}% | {s['mem_peak']:.0f} MiB | {d['cpu_avg']:.0f}% |"
            )
        text, order = ranking(rps, higher_is_better=True)
        summary[label] = (text, " > ".join(NAMES[a] for a in order))
        wins[order[0]] += 1

    out += ["", "## Quem foi mais rápido (req/s)", "", "| Tela | Mais rápido | Ordem |", "|---|---|---|"]
    out += [f"| {label} | {text} | {order} |" for label, (text, order) in summary.items()]
    out += ["", "Vitórias: " + ", ".join(f"{NAMES[a]} {n}" for a, n in wins.items()) + "."]

    meta = {app: json.loads((RESULTS / f"{app}-meta.json").read_text()) for app in apps}
    header = "| | " + " | ".join(NAMES[a] for a in apps) + " |"
    sep = "|---|" + "---:|" * len(apps)
    out += ["", "## Boot, memória e imagem", "", header, sep]
    out.append("| Subir até responder `/up` (inclui migrations) | " + " | ".join(f"{meta[a]['boot_seconds']:.1f} s" for a in apps) + " |")
    out.append("| Memória parado | " + " | ".join(f"{mib(meta[a]['idle_memory']):.0f} MiB" for a in apps) + " |")
    out.append("| Tamanho da imagem Docker | " + " | ".join(f"{meta[a]['image_bytes'] / 1e6:.0f} MB" for a in apps) + " |")

    out += ["", "## Código", "", header, sep]
    out.append("| Linhas de código do app (sem linhas em branco) | " + " | ".join(str(lines_of_code(*CODE[a])) for a in apps) + " |")
    out.append("| Linhas de teste | " + " | ".join(str(lines_of_code(*TESTS[a])) for a in apps) + " |")
    print("\n".join(out))


if __name__ == "__main__":
    main()
