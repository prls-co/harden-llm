"""Create bounded public aggregates from private CPA research measurements."""
import argparse
import collections
from datetime import datetime, timezone, timedelta
import hashlib
import json
import math
from pathlib import Path
import re
import statistics
import subprocess
from zoneinfo import ZoneInfo

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--root', type=Path, required=True)
parser.add_argument('--container', default='cliproxyapi-setup-cli-proxy-api-1')
parser.add_argument('--log-time-zone', default='Asia/Shanghai')
args = parser.parse_args()
ROOT = args.root
CAMPAIGN = ROOT / 'campaign'
OUT = ROOT / 'published'
OUT.mkdir(exist_ok=True, mode=0o700)


def save(name, value):
    path = OUT / name
    path.parent.mkdir(exist_ok=True, parents=True)
    path.write_text(json.dumps(value, indent=2) + '\n')


def stamp(text):
    return datetime.fromisoformat(text.replace('Z', '+00:00'))


def quantile(values, fraction):
    values = sorted(values)
    return values[max(0, math.ceil(len(values) * fraction) - 1)] if values else None


def distribution(values):
    return {'n': len(values), 'median': statistics.median(values) if values else None,
            'p95': quantile(values, .95), 'p99': quantile(values, .99),
            'max': max(values) if values else None}


ledger = json.loads((CAMPAIGN / 'campaign-budget.json').read_text())
summaries = []
correlation = collections.defaultdict(list)
coverage = {}
for row in ledger['runs']:
    name = row['case']
    path = CAMPAIGN / name / 'summary.json'
    if not path.exists():
        continue
    summary = json.loads(path.read_text())
    snapshot = summary['measurement']
    actor = snapshot['Actors'][0]
    events = [json.loads(line) for line in (path.parent / 'events.ndjson').read_text().splitlines()]
    measurements = [e['data'] for e in events if e['type'] == 'call_finished']
    valid = [m for m in measurements if m['kind'] == 'completed']
    first = min(stamp(m['startedAt']) for m in measurements)
    terminal_times = [(stamp(m['finishedAt']) - first).total_seconds() * 1000 for m in valid]
    starts = collections.Counter(int((stamp(m['startedAt']) - first).total_seconds()) for m in measurements)
    kinds = collections.Counter(m['kind'] for m in measurements)
    errors = collections.Counter((m['kind'], m.get('errorCode', ''), m['httpStatus']) for m in measurements if m['kind'] != 'completed')
    nonces = {m.get('validatedNonce') for m in valid}
    qualified = (summary['status'] == 'passed' and len(valid) == actor['Offered']
                 and len(nonces) == actor['Offered'] and len({m['id'] for m in valid}) == actor['Offered'])
    assert sum(m['inputTokens'] + m['outputTokens'] for m in measurements) == snapshot['ReportedTokens']
    entry = {'case': name, 'status': summary['status'], 'allResultsIndependentlyQualified': qualified,
             'offered': actor['Offered'], 'distinctValidatedItems': len(nonces), 'attempts': len(measurements),
             'counts': dict(kinds), 'peak': snapshot['Peak'], 'elapsedMs': snapshot['elapsedMs'],
             'reportedTokens': snapshot['ReportedTokens'], 'inputTokens': actor['InputTokens'],
             'outputTokens': actor['OutputTokens'], 'unknownReserve': snapshot['UnknownTokenReserve'],
             'contextWords': summary['metadata']['contextWords'], 'longEvery': summary['metadata']['longEvery'],
             'inputTokensPerValidResult': distribution([m['inputTokens'] for m in valid]),
             'outputTokensPerValidResult': distribution([m['outputTokens'] for m in valid]),
             'cachedInputTokensReported': sum(m.get('cachedInputTokens') or 0 for m in measurements),
             'cachedInputUsagePresent': sum(m.get('cachedInputTokens') is not None for m in measurements),
             'generationMsValidResults': distribution([m['durationMs'] for m in valid]),
             'nativeHeadersMs': distribution([m['headersMs'] for m in valid if m.get('headersMs') is not None]),
             'nativeFirstOutputMs': distribution([m['firstOutputMs'] for m in valid if m.get('firstOutputMs') is not None]),
             'validCompletionTimeMsFromFirstDispatch': distribution(terminal_times),
             'startsBySecond': dict(sorted(starts.items())),
             'errors': [{'kind': k[0], 'code': k[1], 'status': k[2], 'count': v} for k, v in sorted(errors.items())],
             'sourceSHA': summary['metadata']['sourceSHA'], 'sourceDirty': summary['metadata']['sourceDirty'],
             'nativeCPAStreaming': True, 'TemporalUsed': False,
             'clockOrigin': 'native headers and first-output both start at dispatch; do not mix with HLLM core clocks'}
    save(name + '/summary.json', summary)
    save(name + '/derived.json', entry)
    summaries.append(entry)
    coverage[name] = {'measurementIds': 0, 'matchedIds': set(), 'accessCodes': collections.Counter(),
                      'nativeStatusLogLines': collections.Counter(), 'matchedLogLines': 0}
    for m in measurements:
        if m.get('cpaRequestId'):
            correlation[m['cpaRequestId']].append((name, stamp(m['startedAt']), stamp(m['finishedAt'])))
            coverage[name]['measurementIds'] += 1

# Process logs privately and retain only allowlisted aggregate counters. Never copy raw lines.
zone = subprocess.check_output(['docker', 'exec', args.container, 'printenv', 'TZ']).decode().strip()
if zone != args.log_time_zone:
    raise RuntimeError('log timezone mismatch; verify container timezone before correlation')
process = subprocess.Popen(['docker', 'exec', args.container, 'cat', '/CLIProxyAPI/logs/main.log'], stdout=subprocess.PIPE)
for raw in process.stdout:
    line = raw.decode(errors='replace')
    match = re.search(r'\[([a-f0-9]{8})\]', line)
    if not match or match[1] not in correlation or len(correlation[match[1]]) != 1:
        continue
    name, start, end = correlation[match[1]][0]
    clock = re.search(r'\b(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)\b', line)
    if not clock:
        continue
    at = datetime.strptime(clock[1], '%Y-%m-%d %H:%M:%S').replace(tzinfo=ZoneInfo(args.log_time_zone))
    if at < start - timedelta(seconds=10) or at > end + timedelta(seconds=10):
        continue
    c = coverage[name]
    c['matchedIds'].add(match[1])
    c['matchedLogLines'] += 1
    access = re.search(r'\b(\d{3})\s*\|', line)
    if access:
        c['accessCodes'][access[1]] += 1
    status = re.search(r'(?:status code|status_code|status)\W{0,5}(\d{3})\b', line)
    if status:
        c['nativeStatusLogLines'][status[1]] += 1
assert process.wait() == 0
for name, c in coverage.items():
    c['matchedIds'] = len(c['matchedIds'])
    c['nativeInternalAttempts'] = None
    c['correlationTimeZone'] = args.log_time_zone + '; verified against container TZ; within measured attempt interval plus 10 seconds'
    save(name + '/log-coverage.json', c)
save('matrix.json', {'samples': summaries, 'nativeInternalAttempts': None,
                    'uncontrolledBackgroundTraffic': True, 'sourceFrozenAcrossLiveSamples': len({x['sourceSHA'] for x in summaries}) == 1 and all(not x['sourceDirty'] for x in summaries)})
save('campaign-budget.json', ledger)
for name in ['environment-before.json', 'environment-after.json']:
    p = CAMPAIGN / name
    if p.exists(): save(name, json.loads(p.read_text()))
save('jitter-simulations.json', json.loads((ROOT / 'jitter-derived.json').read_text()))
save('local-verification.json', json.loads((ROOT / 'local-verification.json').read_text()))
print(json.dumps({'samples': len(summaries), 'reportedTokens': sum(s['reportedTokens'] for s in summaries), 'correlatedIds': sum(c['matchedIds'] for c in coverage.values())}))
