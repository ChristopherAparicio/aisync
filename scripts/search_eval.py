#!/usr/bin/env python3
from __future__ import annotations

import argparse
import csv
import json
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable


DEFAULT_QUERIES = Path("eval/search/queries.json")
DEFAULT_BINARY = "aisync"


@dataclass(frozen=True)
class QueryCase:
    id: str
    query: str
    expected_session_ids: list[str]
    category: str = "general"
    notes: str = ""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Evaluate aisync search quality by shelling out to the production binary."
    )
    parser.add_argument(
        "--binary",
        default=DEFAULT_BINARY,
        help="aisync binary to invoke (defaults to 'aisync' on PATH)",
    )
    parser.add_argument(
        "--queries",
        type=Path,
        default=DEFAULT_QUERIES,
        help="Path to JSON query fixture",
    )
    parser.add_argument("--limit", type=int, default=10, help="Top-k results to fetch")
    parser.add_argument(
        "--format",
        choices=("markdown", "json", "csv"),
        default="markdown",
        help="Output format",
    )
    parser.add_argument("--output", type=Path, help="Optional output file")
    return parser.parse_args()


def load_queries(path: Path) -> list[QueryCase]:
    data = json.loads(path.read_text(encoding="utf-8"))
    cases: list[QueryCase] = []
    for item in data["queries"]:
        cases.append(
            QueryCase(
                id=item["id"],
                query=item["query"],
                expected_session_ids=list(item.get("expected_session_ids", [])),
                category=item.get("category", "general"),
                notes=item.get("notes", ""),
            )
        )
    return cases


def run_search(binary: str, query: str, limit: int) -> list[dict[str, Any]]:
    proc = subprocess.run(
        [binary, "list", "--global", "--search", query, "--json", "--limit", str(limit)],
        capture_output=True,
        text=True,
        check=True,
    )
    payload = proc.stdout.strip()
    if not payload:
        return []
    parsed = json.loads(payload)
    if not isinstance(parsed, list):
        raise ValueError(f"expected JSON array from binary, got {type(parsed).__name__}")
    return parsed


def query_index(binary: str, case: QueryCase, *, limit: int) -> list[dict[str, Any]]:
    rows = run_search(binary, case.query, limit)
    results: list[dict[str, Any]] = []
    for rank, row in enumerate(rows, start=1):
        results.append(
            {
                "rank": rank,
                "session_id": row.get("id", ""),
                "summary": row.get("summary", ""),
                "project_path": row.get("project_path", ""),
                "branch": row.get("branch", ""),
                "provider": row.get("provider", ""),
                "total_tokens": row.get("total_tokens", 0),
                "message_count": row.get("message_count", 0),
            }
        )
    return results


def first_relevant_rank(case: QueryCase, results: Iterable[dict[str, Any]]) -> int | None:
    expected = set(case.expected_session_ids)
    if not expected:
        return None
    for result in results:
        if result["session_id"] in expected:
            return int(result["rank"])
    return None


def evaluate(cases: list[QueryCase], binary: str, limit: int) -> dict[str, Any]:
    rows: list[dict[str, Any]] = []
    known_ranks: list[int | None] = []
    for case in cases:
        results = query_index(binary, case, limit=limit)
        rank = first_relevant_rank(case, results)
        if case.expected_session_ids:
            known_ranks.append(rank)
        rows.append(
            {
                "id": case.id,
                "query": case.query,
                "category": case.category,
                "notes": case.notes,
                "expected_session_ids": case.expected_session_ids,
                "first_relevant_rank": rank,
                "hits": results,
            }
        )

    total = len(known_ranks)
    metrics = {
        "known_queries": total,
        "p_at_1": precision_at(known_ranks, 1),
        "p_at_3": precision_at(known_ranks, 3),
        "p_at_5": precision_at(known_ranks, 5),
        "p_at_10": precision_at(known_ranks, 10),
        "mrr": mean_reciprocal_rank(known_ranks),
    }
    return {"metrics": metrics, "results": rows}


def precision_at(ranks: list[int | None], k: int) -> float:
    if not ranks:
        return 0.0
    return sum(1 for rank in ranks if rank is not None and rank <= k) / len(ranks)


def mean_reciprocal_rank(ranks: list[int | None]) -> float:
    if not ranks:
        return 0.0
    return sum(1.0 / rank if rank else 0.0 for rank in ranks) / len(ranks)


def render_markdown(report: dict[str, Any]) -> str:
    metrics = report["metrics"]
    lines = [
        "# aisync Search Evaluation",
        "",
        "## Metrics",
        "",
        f"- Known queries: {metrics['known_queries']}",
        f"- P@1: {metrics['p_at_1']:.3f}",
        f"- P@3: {metrics['p_at_3']:.3f}",
        f"- P@5: {metrics['p_at_5']:.3f}",
        f"- P@10: {metrics['p_at_10']:.3f}",
        f"- MRR: {metrics['mrr']:.3f}",
        "",
        "## Queries",
        "",
        "| ID | Query | Category | Rank | Top hit | Notes |",
        "|---|---|---|---:|---|---|",
    ]
    for row in report["results"]:
        top_hit = row["hits"][0]["session_id"] if row["hits"] else ""
        rank = row["first_relevant_rank"] if row["first_relevant_rank"] is not None else ""
        lines.append(
            "| {id} | {query} | {category} | {rank} | {top_hit} | {notes} |".format(
                id=escape_md(row["id"]),
                query=escape_md(row["query"]),
                category=escape_md(row["category"]),
                rank=rank,
                top_hit=escape_md(top_hit),
                notes=escape_md(row["notes"]),
            )
        )
    return "\n".join(lines) + "\n"


def render_csv(report: dict[str, Any]) -> str:
    from io import StringIO

    output = StringIO()
    writer = csv.DictWriter(
        output,
        fieldnames=[
            "id",
            "query",
            "category",
            "first_relevant_rank",
            "expected_session_ids",
            "top_hit",
            "top_hit_summary",
            "notes",
        ],
    )
    writer.writeheader()
    for row in report["results"]:
        top = row["hits"][0] if row["hits"] else {}
        writer.writerow(
            {
                "id": row["id"],
                "query": row["query"],
                "category": row["category"],
                "first_relevant_rank": row["first_relevant_rank"] or "",
                "expected_session_ids": " ".join(row["expected_session_ids"]),
                "top_hit": top.get("session_id", ""),
                "top_hit_summary": top.get("summary", ""),
                "notes": row["notes"],
            }
        )
    return output.getvalue()


def escape_md(value: Any) -> str:
    return str(value).replace("|", "\\|").replace("\n", " ")


def main() -> int:
    args = parse_args()
    cases = load_queries(args.queries)
    report = evaluate(cases, args.binary, args.limit)

    if args.format == "json":
        rendered = json.dumps(report, indent=2, ensure_ascii=False) + "\n"
    elif args.format == "csv":
        rendered = render_csv(report)
    else:
        rendered = render_markdown(report)

    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(rendered, encoding="utf-8")
    else:
        print(rendered, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
