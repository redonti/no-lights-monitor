# ── Layout ─────────────────────────────────────────────────────────────────────
W      = 1100
PAD_L  = 130
PAD_R  = 160
PAD_T  = 72     # room for title
PAD_B  = 56
BAR_W  = W - PAD_L - PAD_R
BAR_H  = 40
BAR_R  = 5
ROW_H  = 72
BX     = PAD_L

# ── Palette ────────────────────────────────────────────────────────────────────
C_ON   = '#34A853'
C_OFF  = '#EA4335'
C_GRAY = '#E0E0E0'
C_TEXT = '#1A1A1A'
C_SUB  = '#888888'
C_AXIS = '#999999'
CMAP   = {'green': C_ON, 'red': C_OFF, 'unknown': C_GRAY, 'future': C_GRAY}


def _fmt(hours: float) -> str:
    h = int(hours)
    m = int(round((hours - h) * 60))
    if m == 60:
        h += 1; m = 0
    return f"{h}\u0433\u043e\u0434 {m}\u0445\u0432"


def _px(hour: float) -> float:
    return hour / 24.0 * BAR_W


def _date(label: str) -> str:
    """Extract date from label like 'ПН (16.02)' → '16.02'"""
    return label.split('(')[1].rstrip(')') if '(' in label else label


def draw_chart(days: list) -> bytes:
    N     = len(days)
    SVG_H = PAD_T + N * ROW_H + PAD_B

    o = []
    o.append(
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{SVG_H}" '
        f'style="font-family:DejaVu Sans,Arial,sans-serif;">'
    )
    # explicit white bg — cairosvg ignores CSS background
    o.append(f'<rect width="{W}" height="{SVG_H}" fill="#ffffff"/>')

    # ── title ──────────────────────────────────────────────────────────────────
    d_from = _date(days[0]['date_label'])
    d_to   = _date(days[-1]['date_label'])
    title  = (
        "\u0413\u0440\u0430\u0444\u0456\u043a "
        "\u0432\u0456\u0434\u043a\u043b\u044e\u0447\u0435\u043d\u044c "
        "\u0441\u0432\u0456\u0442\u043b\u0430"
    )
    o.append(
        f'<text x="{W // 2}" y="36" text-anchor="middle" '
        f'font-size="22" font-weight="bold" fill="{C_TEXT}">'
        f'{title}  {d_from} \u2013 {d_to}</text>'
    )

    # ── day rows ───────────────────────────────────────────────────────────────
    for i, day in enumerate(days):
        by  = PAD_T + i * ROW_H
        mid = by + BAR_H // 2

        # day label
        name, date = day['date_label'].split(' ', 1)
        o.append(
            f'<text x="{BX - 10}" y="{mid - 6}" text-anchor="end" '
            f'font-size="17" font-weight="bold" fill="{C_TEXT}">{name}</text>'
        )
        o.append(
            f'<text x="{BX - 10}" y="{mid + 13}" text-anchor="end" '
            f'font-size="13" fill="{C_SUB}">{date}</text>'
        )

        # bar bg
        o.append(
            f'<rect x="{BX}" y="{by}" width="{BAR_W}" height="{BAR_H}" '
            f'rx="{BAR_R}" fill="{C_GRAY}"/>'
        )

        # segments
        if day.get('segments'):
            cid = f'c{i}'
            o.append(
                f'<clipPath id="{cid}"><rect x="{BX}" y="{by}" '
                f'width="{BAR_W}" height="{BAR_H}" rx="{BAR_R}"/></clipPath>'
            )
            for seg in day['segments']:
                sx = BX + _px(seg['start'])
                sw = _px(seg['end'] - seg['start'])
                if sw <= 0:
                    continue
                o.append(
                    f'<rect x="{sx:.1f}" y="{by}" width="{sw:.1f}" height="{BAR_H}" '
                    f'fill="{CMAP.get(seg["kind"], C_GRAY)}" clip-path="url(#{cid})"/>'
                )

        # ticks — bottom of bar upward
        for h in range(25):
            tx     = BX + _px(h)
            is_maj = h % 3 == 0
            th     = 16 if is_maj else 8
            sw     = '2' if is_maj else '1.5'
            col    = 'rgba(0,0,0,0.30)' if is_maj else 'rgba(0,0,0,0.14)'
            o.append(
                f'<line x1="{tx:.1f}" y1="{by + BAR_H - th}" '
                f'x2="{tx:.1f}" y2="{by + BAR_H}" '
                f'stroke="{col}" stroke-width="{sw}"/>'
            )

        # hour labels
        for h in [0, 3, 6, 9, 12, 15, 18, 21, 24]:
            tx     = BX + _px(h)
            anchor = 'start' if h == 0 else 'end' if h == 24 else 'middle'
            o.append(
                f'<text x="{tx:.1f}" y="{by + BAR_H + 20}" '
                f'text-anchor="{anchor}" font-size="14" fill="{C_AXIS}">{h}</text>'
            )

        # right stats
        rx = BX + BAR_W + 12
        if not day.get('is_future'):
            o.append(
                f'<text x="{rx}" y="{mid - 4}" font-size="14" font-weight="bold" '
                f'fill="{C_ON}">&#9650; {_fmt(day["hours_online"])}</text>'
            )
            o.append(
                f'<text x="{rx}" y="{mid + 16}" font-size="14" font-weight="bold" '
                f'fill="{C_OFF}">&#9660; {_fmt(day["hours_offline"])}</text>'
            )
        else:
            o.append(
                f'<text x="{rx}" y="{mid + 6}" font-size="14" fill="{C_AXIS}">&#8212;</text>'
            )

    # ── footer ─────────────────────────────────────────────────────────────────
    fy        = PAD_T + N * ROW_H + 32
    total_on  = sum(d['hours_online']  for d in days if not d.get('is_future'))
    total_off = sum(d['hours_offline'] for d in days if not d.get('is_future'))
    total     = total_on + total_off
    p_on      = total_on  / total * 100 if total else 0
    p_off     = total_off / total * 100 if total else 0
    outages   = sum(1 for d in days for s in d.get('segments', []) if s['kind'] == 'red')

    col1 = BX
    col2 = BX + BAR_W // 3
    col3 = BX + BAR_W * 2 // 3

    o.append(
        f'<text x="{col1}" y="{fy}" font-size="15" fill="{C_TEXT}">'
        f'\u0417\u0456 \u0441\u0432\u0456\u0442\u043b\u043e\u043c '
        f'<tspan font-weight="bold" fill="{C_ON}">{_fmt(total_on)}</tspan>'
        f' <tspan fill="{C_SUB}" font-size="13">({p_on:.1f}%)</tspan></text>'
    )
    o.append(
        f'<text x="{col2}" y="{fy}" font-size="15" fill="{C_TEXT}">'
        f'\u0411\u0435\u0437 \u0441\u0432\u0456\u0442\u043b\u0430 '
        f'<tspan font-weight="bold" fill="{C_OFF}">{_fmt(total_off)}</tspan>'
        f' <tspan fill="{C_SUB}" font-size="13">({p_off:.1f}%)</tspan></text>'
    )
    o.append(
        f'<text x="{col3}" y="{fy}" font-size="15" fill="{C_TEXT}">'
        f'\u0412\u0456\u0434\u043a\u043b\u044e\u0447\u0435\u043d\u044c '
        f'<tspan font-weight="bold">{outages}</tspan></text>'
    )

    o.append('</svg>')

    svg_bytes = ''.join(o).encode('utf-8')

    import cairosvg
    return cairosvg.svg2png(bytestring=svg_bytes, scale=2)