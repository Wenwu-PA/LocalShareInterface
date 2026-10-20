"""Conservative, reproducible memory-growth verdict for a completed daily run."""
import math
import statistics


def memory_verdict(samples, completed):
    incomplete={'verdict':'INCOMPLETE','message':'Недостаточно данных: суточный прогон не подтверждён'}
    if not completed or len(samples)<3: return incomplete
    times=[s['seconds'] for s in samples]
    if any(not math.isfinite(t) for t in times) or times[0]>120 or times[-1]<86400: return incomplete
    if any(b<=a or b-a>120 for a,b in zip(times,times[1:])): return incomplete
    if any(not math.isfinite(s['rss']) or s['rss']<=0 for s in samples): return incomplete
    baseline=[s['rss'] for s in samples if 3600<=s['seconds']<7200]
    final=[s['rss'] for s in samples if times[-1]-3600<=s['seconds']<=times[-1]]
    if len(baseline)<30 or len(final)<30:return incomplete
    first=statistics.median(baseline);last=statistics.median(final)
    growth=(last/first-1)*100
    leak=growth>20
    return {'verdict':'GROWTH' if leak else 'STABLE','baseline_median_rss':first,
            'final_median_rss':last,'growth_percent':round(growth,3),'threshold_percent':20,
            'message':'Утечка есть по критерию роста RAM >20%' if leak else 'Утечки нет по критерию роста RAM >20%'}
