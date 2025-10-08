import re
import sys
import numpy as np

pattern = re.compile(r"duration:\s+(\d+.\d+)\s+ms")
durations: list[float] = []

for d in sys.stdin.readlines():
    m = pattern.search(d)
    if m is None:
        continue
    durations.append(float(m.group(1)))

np_durations = np.array(durations)

p90 = np.percentile(np_durations, 90)
p95 = np.percentile(np_durations, 95)
p99 = np.percentile(np_durations, 99)

print(f"P90 = {p90:.3f} ms")
print(f"P95 = {p95:.3f} ms")
print(f"P99 = {p99:.3f} ms")
