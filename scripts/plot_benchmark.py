#!/usr/bin/env python3
"""Generate benchmark chart from loadtest JSON results."""

import json
import sys
from pathlib import Path

try:
    import matplotlib.pyplot as plt
    import matplotlib.patches as mpatches
    import numpy as np
except ImportError:
    print("Install dependencies: pip install matplotlib numpy")
    sys.exit(1)

results_dir = Path(__file__).parent.parent / "results"
files = {
    "c=10":  results_dir / "loadtest_c10.json",
    "c=25":  results_dir / "loadtest_c25.json",
    "c=50":  results_dir / "loadtest_c50.json",
    "c=75":  results_dir / "loadtest_c75.json",
    "c=100": results_dir / "loadtest_c100.json",
}

data = {}
for label, path in files.items():
    if not path.exists():
        print(f"Missing {path} — run the load test first")
        sys.exit(1)
    with open(path) as f:
        data[label] = json.load(f)

concurrency_labels = list(data.keys())
tiers = ["high", "medium", "low"]
colors = {"high": "#f85149", "medium": "#d29922", "low": "#3fb950"}

p95 = {tier: [data[c]["tiers"][tier]["p95_ms"] / 1000 for c in concurrency_labels] for tier in tiers}

x = np.arange(len(concurrency_labels))
width = 0.25

fig, ax = plt.subplots(figsize=(9, 5))
fig.patch.set_facecolor("#0d1117")
ax.set_facecolor("#161b22")

for i, tier in enumerate(tiers):
    bars = ax.bar(x + i * width, p95[tier], width, color=colors[tier], alpha=0.9, zorder=3)
    for bar, val in zip(bars, p95[tier]):
        ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 0.3,
                f"{val:.1f}s", ha="center", va="bottom", fontsize=8,
                color=colors[tier], fontweight="bold")

ax.set_xlabel("Concurrent Clients", color="#8b949e", labelpad=8)
ax.set_ylabel("p95 Latency (seconds)", color="#8b949e", labelpad=8)
ax.set_title("PriorityServe — p95 Latency by Priority Tier", color="#e6edf3",
             fontsize=13, fontweight="bold", pad=14)

ax.set_xticks(x + width)
ax.set_xticklabels(concurrency_labels, color="#e6edf3")
ax.tick_params(colors="#8b949e")
ax.yaxis.label.set_color("#8b949e")
for spine in ax.spines.values():
    spine.set_edgecolor("#30363d")
ax.tick_params(axis="both", colors="#8b949e")
ax.grid(axis="y", color="#21262d", linewidth=0.8, zorder=0)

legend_patches = [mpatches.Patch(color=colors[t], label=t.capitalize()) for t in tiers]
ax.legend(handles=legend_patches, facecolor="#161b22", edgecolor="#30363d",
          labelcolor="#e6edf3", fontsize=9)

out = results_dir / "benchmark.png"
plt.tight_layout()
plt.savefig(out, dpi=150, facecolor=fig.get_facecolor())
print(f"Saved to {out}")
plt.show()
