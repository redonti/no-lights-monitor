from fastapi import FastAPI, HTTPException
from fastapi.responses import Response
from pydantic import BaseModel, Field
from typing import List, Optional
from datetime import datetime, timedelta, timezone
from zoneinfo import ZoneInfo
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import io

plt.rcParams['font.family'] = 'DejaVu Sans'
plt.rcParams['font.size'] = 12

KYIV_TZ      = ZoneInfo('Europe/Kyiv')
DAY_NAMES_UA = ['ПН', 'ВТ', 'СР', 'ЧТ', 'ПТ', 'СБ', 'НД']

COLOR_GREEN      = '#90EE90'
COLOR_RED        = '#FF6B6B'
COLOR_UNKNOWN    = '#E8E8E8'   # gray — no data / new monitor
COLOR_FUTURE     = '#E8E8E8'   # gray — future hours
COLOR_GRID_LIGHT = '#F0F0F0'
COLOR_GRID_HEAVY = '#DDDDDD'

app = FastAPI(
    title="Light Status Graph Service",
    description="Generates weekly light status graphs from monitoring data",
    version="2.0.0"
)


# ── Models: new simple endpoint ───────────────────────────────────────────────

class RawEvent(BaseModel):
    id:         int
    monitor_id: int
    is_online:  bool
    timestamp:  str


class WeekFromEventsRequest(BaseModel):
    monitor_id: int            = Field(..., description="Monitor ID")
    week_start: str            = Field(..., description="Monday 00:00 UTC e.g. '2026-02-09T00:00:00Z'")
    events:     List[RawEvent] = Field(..., description=(
        "All events from at least (week_start - 1 day) up to now. "
        "Include at least one event BEFORE week_start so Monday's "
        "initial status can be auto-detected. "
        "For new monitors with no history, omit pre-week events and "
        "the graph will show gray until the first known event."
    ))

    class Config:
        json_schema_extra = {
            "example": {
                "monitor_id": 1,
                "week_start": "2026-02-09T00:00:00Z",
                "events": [
                    {"id": 0, "monitor_id": 1, "is_online": True,  "timestamp": "2026-02-08T20:00:00Z"},
                    {"id": 1, "monitor_id": 1, "is_online": False, "timestamp": "2026-02-09T08:30:00Z"},
                    {"id": 2, "monitor_id": 1, "is_online": True,  "timestamp": "2026-02-09T12:15:00Z"},
                ]
            }
        }


# ── Models: legacy pre-processed endpoint ─────────────────────────────────────

class DaySegment(BaseModel):
    start_hour: float = Field(..., ge=0, le=24)
    end_hour:   float = Field(..., ge=0, le=24)
    is_online:  bool

class DayData(BaseModel):
    date: str;  day_name: str;  segments: List[DaySegment]
    hours_online: float;  hours_offline: float

class WeekGraphRequest(BaseModel):
    monitor_id: int;  week_start: str;  week_end: str;  days: List[DayData]


# ── Helpers ────────────────────────────────────────────────────────────────────

def parse_ts(ts: str) -> datetime:
    return datetime.fromisoformat(ts.replace('Z', '+00:00')).astimezone(KYIV_TZ)

def to_decimal(dt: datetime) -> float:
    return dt.hour + dt.minute / 60.0 + dt.second / 3600.0

def format_hours(hours: float) -> str:
    h = int(hours)
    m = int(round((hours - h) * 60))
    if m == 60:
        h += 1; m = 0
    return f"{h}год {m}хв"

def seg_kind(status: Optional[bool]) -> str:
    """Convert Optional[bool] status to segment kind string."""
    if status is None:  return 'unknown'   # new monitor — no history yet
    return 'green' if status else 'red'


# ── Segment builder ────────────────────────────────────────────────────────────

def build_day_segments(
    day_events: list,
    initial_status: Optional[bool],   # ← None for new monitors with no history
    cutoff: Optional[float],
) -> tuple[list, Optional[bool]]:
    """
    Convert sorted intra-day events into drawable segments.

    Kinds:
        'green'   — lights ON  (known past/present)
        'red'     — lights OFF (known past/present)
        'unknown' — no history yet (new monitor), drawn gray
        'future'  — after current time, drawn gray

    initial_status=None means no pre-week event exists (new monitor).
    Gray is drawn until the first in-week event fires and gives us real data.
    Returns (segments, last_status) where last_status may still be None.
    """
    segments  = []
    cursor    = 0.0
    status    = initial_status
    known_end = cutoff if cutoff is not None else 24.0

    for ev in day_events:
        if ev['hour'] >= known_end:
            break
        if ev['hour'] > cursor:
            segments.append({'start': cursor, 'end': ev['hour'], 'kind': seg_kind(status)})
        status = ev['is_online']   # first event resolves None → real bool
        cursor = ev['hour']

    if cursor < known_end:
        segments.append({'start': cursor, 'end': known_end, 'kind': seg_kind(status)})

    if cutoff is not None and cutoff < 24.0:
        segments.append({'start': cutoff, 'end': 24.0, 'kind': 'future'})

    return segments, status


# ── Drawing ────────────────────────────────────────────────────────────────────

def draw_chart(days: list) -> bytes:
    n = len(days)
    fig, axes = plt.subplots(n, 1, figsize=(16, n * 1.2), sharex=False, sharey=False)
    if n == 1:
        axes = [axes]
    fig.patch.set_facecolor('white')

    color_map = {
        'green':   COLOR_GREEN,
        'red':     COLOR_RED,
        'unknown': COLOR_UNKNOWN,
        'future':  COLOR_FUTURE,
    }

    for ax, day in zip(axes, days):
        ax.set_facecolor('white')

        for seg in day['segments']:
            ax.barh(0, seg['end'] - seg['start'], left=seg['start'],
                    height=0.6, color=color_map[seg['kind']], edgecolor='none')

        ax.text(-1.2, 0, day['date_label'],
                va='center', ha='right', fontsize=14, color='#999999')

        if not day['is_future']:
            ax.text(25.5,  0.2, format_hours(day['hours_online']),
                    va='center', ha='left', fontsize=11, color='#4CAF50', fontweight='bold')
            ax.text(25.5, -0.2, format_hours(day['hours_offline']),
                    va='center', ha='left', fontsize=11, color='#FF5252', fontweight='bold')
        else:
            ax.text(25.5, 0, '—', va='center', ha='left', fontsize=11, color='#BBBBBB')

        ax.set_xlim(-1.5, 27)
        ax.set_ylim(-0.5, 0.5)
        ax.set_xticks([0, 3, 6, 9, 12, 15, 18, 21, 24])
        ax.set_xticklabels(['0','3','6','9','12','15','18','21','24'], fontsize=12, color='#AAAAAA')
        ax.set_yticks([])

        for h in range(25):
            ax.axvline(x=h, color=COLOR_GRID_LIGHT, linewidth=0.8, zorder=0)
        for h in [0, 3, 6, 9, 12, 15, 18, 21, 24]:
            ax.axvline(x=h, color=COLOR_GRID_HEAVY, linewidth=1.2, zorder=0)

        for spine in ax.spines.values():
            spine.set_visible(False)
        ax.tick_params(axis='x', length=0)

    plt.tight_layout(h_pad=0.5)
    buf = io.BytesIO()
    plt.savefig(buf, format='png', dpi=200, bbox_inches='tight', facecolor='white')
    plt.close(fig)
    buf.seek(0)
    return buf.read()


# ── Core logic: /generate-week-graph ─────────────────────────────────────────

def build_week_from_events(req: WeekFromEventsRequest) -> bytes:
    week_start_dt = parse_ts(req.week_start)
    week_end_dt   = week_start_dt + timedelta(days=7)
    now_kyiv      = datetime.now(KYIV_TZ)

    all_sorted = sorted(req.events, key=lambda e: parse_ts(e.timestamp))

    # Initial status: last event BEFORE week_start.
    # None = no history (new monitor) → gray until first real event.
    pre_week       = [e for e in all_sorted if parse_ts(e.timestamp) < week_start_dt]
    initial_status = pre_week[-1].is_online if pre_week else None

    # Group week events by Kyiv calendar day
    by_day: dict[str, list] = {}
    for ev in all_sorted:
        ev_dt = parse_ts(ev.timestamp)
        if ev_dt < week_start_dt or ev_dt >= week_end_dt:
            continue
        key = ev_dt.strftime('%Y-%m-%d')
        by_day.setdefault(key, []).append({
            'hour':      to_decimal(ev_dt),
            'is_online': ev.is_online,
        })

    days_draw: list[dict] = []
    carry = initial_status   # None for new monitor; real bool once first event fires

    for offset in range(7):
        day_dt    = week_start_dt + timedelta(days=offset)
        day_key   = day_dt.strftime('%Y-%m-%d')
        label     = f"{DAY_NAMES_UA[day_dt.weekday()]} ({day_dt.strftime('%d.%m')})"
        this_date  = day_dt.date()
        today_kyiv = now_kyiv.date()

        if this_date > today_kyiv:
            days_draw.append({
                'date_label':    label,
                'segments':      [{'start': 0.0, 'end': 24.0, 'kind': 'future'}],
                'hours_online':  0.0,
                'hours_offline': 0.0,
                'is_future':     True,
            })

        else:
            cutoff     = to_decimal(now_kyiv) if this_date == today_kyiv else None
            day_events = sorted(by_day.get(day_key, []), key=lambda x: x['hour'])
            segments, last_status = build_day_segments(day_events, carry, cutoff)

            hours_online  = sum(s['end'] - s['start'] for s in segments if s['kind'] == 'green')
            hours_offline = sum(s['end'] - s['start'] for s in segments if s['kind'] == 'red')
            has_real_data = any(s['kind'] in ('green', 'red') for s in segments)

            days_draw.append({
                'date_label':    label,
                'segments':      segments,
                'hours_online':  round(hours_online, 2),
                'hours_offline': round(hours_offline, 2),
                'is_future':     not has_real_data,   # hide stats if entirely unknown
            })
            carry = last_status

    return draw_chart(days_draw)


# ── Core logic: /generate-graph (legacy) ──────────────────────────────────────

def build_legacy(request: WeekGraphRequest) -> bytes:
    days = [{
        'date_label':    f"{d.day_name} ({d.date})",
        'segments':      [{'start': s.start_hour, 'end': s.end_hour,
                           'kind': 'green' if s.is_online else 'red'} for s in d.segments],
        'hours_online':  d.hours_online,
        'hours_offline': d.hours_offline,
        'is_future':     False,
    } for d in request.days]
    return draw_chart(days)


# ── Endpoints ──────────────────────────────────────────────────────────────────

@app.get("/health")
async def health_check():
    return {
        "status":    "healthy",
        "service":   "light-status-graph",
        "version":   "2.0.0",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "now_kyiv":  datetime.now(KYIV_TZ).strftime('%Y-%m-%d %H:%M:%S %Z'),
    }


@app.post("/generate-week-graph", response_class=Response,
          responses={200: {"content": {"image/png": {}},
                           "description": "Full Mon-Sun PNG. Past=green/red. Unknown/future=gray."}},
          summary="Generate weekly graph from raw events (recommended)")
async def generate_week_graph(request: WeekFromEventsRequest):
    """
    Pass week_start + raw events (include at least one event before week_start
    for known monitors). New monitors with no pre-week events will render gray
    until the first real event fires.
    """
    try:
        return Response(content=build_week_from_events(request), media_type="image/png")
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/generate-graph", response_class=Response,
          responses={200: {"content": {"image/png": {}}}},
          summary="Generate graph from pre-processed segments (legacy)")
async def generate_graph(request: WeekGraphRequest):
    try:
        return Response(content=build_legacy(request), media_type="image/png")
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/")
async def root():
    return {
        "service": "Light Status Graph Generator",
        "version": "2.0.0",
        "endpoints": {
            "POST /generate-week-graph": "raw events -> full Mon-Sun graph (recommended)",
            "POST /generate-graph":      "pre-processed segments (legacy)",
            "GET  /health":              "health check",
            "GET  /docs":                "Swagger UI",
        },
    }