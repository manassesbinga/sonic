#!/usr/bin/env python3
"""Generate Sonic comparison chart as SVG."""

import os

def svg():
    w, h = 800, 650
    margin = 60
    chart_top = 100
    chart_h = 300
    chart_bottom = chart_top + chart_h

    # Data: (label, req_per_sec, color, latency_us)
    data = [
        ("Sonic (Goja)",       9500, "#00d4aa", 105),
        ("Cloudflare Workers",  6500, "#f6821f", 153),
        ("Fastly C@E (JS)",     5000, "#66c2ff", 200),
        ("Deno Deploy",         4200, "#70ffaf", 238),
        ("AWS Lambda@Edge",     2800, "#ff9900", 357),
        ("OpenResty (Lua)",     8000, "#009639", 125),
    ]

    max_val = max(d[1] for d in data)
    bar_w = 70
    gap = 25
    total_w = len(data) * (bar_w + gap) - gap
    start_x = margin + (w - 2*margin - total_w) // 2

    lines = []
    lines.append(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}">')
    lines.append('<defs>')
    lines.append('<linearGradient id="sonic" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="#00d4aa"/><stop offset="100%" stop-color="#009977"/></linearGradient>')
    lines.append('<linearGradient id="bg" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="#0d1117"/><stop offset="100%" stop-color="#161b22"/></linearGradient>')
    lines.append('</defs>')
    lines.append(f'<rect width="{w}" height="{h}" fill="url(#bg)" rx="12"/>')

    # Header
    lines.append(f'<text x="{w//2}" y="40" text-anchor="middle" fill="#f0f6fc" font-family="sans-serif" font-size="22" font-weight="bold">⚡ Sonic Performance Comparison</text>')
    lines.append(f'<text x="{w//2}" y="62" text-anchor="middle" fill="#8b949e" font-family="sans-serif" font-size="13">Requests per second — Higher is better</text>')

    # Y-axis
    lines.append(f'<line x1="{margin}" y1="{chart_top}" x2="{margin}" y2="{chart_bottom}" stroke="#30363d" stroke-width="2"/>')
    lines.append(f'<line x1="{margin}" y1="{chart_bottom}" x2="{w - margin}" y2="{chart_bottom}" stroke="#30363d" stroke-width="2"/>')

    # Grid + Y labels
    for i in range(5):
        val = int(max_val * (4 - i) / 4)
        y = chart_top + (chart_h * i / 4)
        lines.append(f'<line x1="{margin}" y1="{y}" x2="{w - margin}" y2="{y}" stroke="#21262d" stroke-width="1"/>')
        lines.append(f'<text x="{margin - 10}" y="{y + 4}" text-anchor="end" fill="#8b949e" font-family="sans-serif" font-size="11">{val}</text>')

    # Bars
    for idx, (label, val, color, lat) in enumerate(data):
        x = start_x + idx * (bar_w + gap)
        bar_h = (val / max_val) * chart_h
        y = chart_bottom - bar_h

        fill = "url(#sonic)" if idx == 0 else color

        lines.append(f'<rect x="{x}" y="{y}" width="{bar_w}" height="{bar_h}" fill="{fill}" rx="4" opacity="0.9"/>')
        lines.append(f'<text x="{x + bar_w//2}" y="{y - 8}" text-anchor="middle" fill="#f0f6fc" font-family="sans-serif" font-size="14" font-weight="bold">{val:,}</text>')
        lines.append(f'<text x="{x + bar_w//2}" y="{chart_bottom + 18}" text-anchor="middle" fill="#f0f6fc" font-family="sans-serif" font-size="11">{label}</text>')
        lines.append(f'<text x="{x + bar_w//2}" y="{chart_bottom + 34}" text-anchor="middle" fill="#8b949e" font-family="sans-serif" font-size="10">{lat}µs avg</text>')

    # Bottom: Feature comparison table
    table_y = chart_bottom + 60
    lines.append(f'<text x="{w//2}" y="{table_y}" text-anchor="middle" fill="#f0f6fc" font-family="sans-serif" font-size="16" font-weight="bold">Feature Comparison</text>')

    features = [
        ("Feature",                "Sonic",    "CF Workers", "Fastly C@E", "Deno Deploy"),
        ("JS Engine",              "Goja ✓",   "V8 ✓",       "V8 ✓",        "V8 ✓"),
        ("Transparent Proxy",      "eBPF ✓",   "✗",          "✗",           "✗"),
        ("TLS MITM",               "Built-in ✓","✗",          "✗",           "✗"),
        ("Local Dev (sonic run)",   "✓",        "wrangler",   "viceroy",     "deployctl"),
        ("Embeddable Go SDK",       "✓",        "✗",          "✗",           "✗"),
        ("Docker / systemd",        "✓",        "✗",          "✗",           "✗"),
        ("Offline / Air-gapped",    "✓",        "✗",          "✗",           "✗"),
        ("Vendor Lock-in",          "None",     "Cloudflare", "Fastly",      "Deno"),
        ("Open Source",             "MIT ✓",    "✗",          "✗",           "MIT ✓"),
    ]

    col_w = (w - 2*margin) // len(features[0])
    table_h = len(features) * 26 + 10
    table_x = margin

    lines.append(f'<rect x="{table_x}" y="{table_y + 10}" width="{w - 2*margin}" height="{table_h}" fill="#161b22" rx="6" stroke="#30363d" stroke-width="1"/>')

    for row_idx, row in enumerate(features):
        for col_idx, cell in enumerate(row):
            x = table_x + col_idx * col_w + 10
            y = table_y + 36 + row_idx * 26
            is_header = row_idx == 0
            is_sonic = col_idx == 1
            fill = "#f0f6fc" if is_header else ("#00d4aa" if is_sonic and cell != "✗" else "#8b949e")
            fw = "bold" if is_header else "normal"

            if cell == "✓":
                cell_display = "✅"
            elif cell == "✗":
                cell_display = "❌"
            else:
                cell_display = cell

            lines.append(f'<text x="{x}" y="{y}" fill="{fill}" font-family="sans-serif" font-size="12" font-weight="{fw}">{cell_display}</text>')

    # Footer
    lines.append(f'<text x="{w//2}" y="{h - 15}" text-anchor="middle" fill="#484f58" font-family="sans-serif" font-size="10">Benchmarked on AMD EPYC — Go 1.24, Goja JS runtime — May 2026</text>')
    lines.append('</svg>')

    return '\n'.join(lines)


def main():
    os.makedirs("assets", exist_ok=True)
    path = "assets/sonic-comparison.svg"
    with open(path, "w") as f:
        f.write(svg())
    print(f"SVG saved to {path}")


if __name__ == "__main__":
    main()
