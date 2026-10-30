import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Card, Empty, ErrorState, Loading, Page } from "../components/ui";
import { api } from "../lib/api";
import { dateTime } from "../lib/format";

interface Entry {
  id: number;
  actor_label: string;
  action: string;
  entity: string;
  entity_id: string;
  before_data: unknown;
  after_data: unknown;
  created_at: string;
}

const entities = ["", "reservation", "folio", "room", "hk_task", "channel_booking", "night_audit", "assignment"];

export default function AuditLog() {
  const [entity, setEntity] = useState("");
  const [open, setOpen] = useState<number | null>(null);
  const query = useInfiniteQuery({
    queryKey: ["audit-log", entity],
    initialPageParam: null as number | null,
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "40" });
      if (entity) params.set("entity", entity);
      if (pageParam) params.set("cursor", String(pageParam));
      return api<{ items: Entry[]; next_cursor: number | null }>(`/audit-log?${params}`);
    },
    getNextPageParam: (last) => last.next_cursor,
  });
  const rows = query.data?.pages.flatMap((page) => page.items) ?? [];

  return (
    <Page
      title="Denetim Kaydı"
      note="Kayıtlar yalnızca eklenebilir, değiştirilemez ve silinemez."
      actions={
        <select value={entity} onChange={(e) => setEntity(e.target.value)} aria-label="Kayıt türü">
          {entities.map((value) => (
            <option key={value} value={value}>
              {value || "Tüm kayıtlar"}
            </option>
          ))}
        </select>
      }
    >
      <Card flush>
        {query.isPending ? (
          <Loading />
        ) : query.error ? (
          <ErrorState error={query.error} retry={() => void query.refetch()} />
        ) : rows.length === 0 ? (
          <Empty title="Kayıt yok" />
        ) : (
          <ul className="rows">
            {rows.map((row) => (
              <li key={row.id}>
                <button className="row-button" onClick={() => setOpen(open === row.id ? null : row.id)} aria-expanded={open === row.id}>
                  <span>
                    <strong>{row.action}</strong>
                    <span className="muted small">
                      {row.actor_label} · {row.entity} #{row.entity_id}
                    </span>
                  </span>
                  <span className="muted small">{dateTime(row.created_at)}</span>
                </button>
                {open === row.id && (
                  <div className="diff">
                    <div>
                      <span className="muted small">Önce</span>
                      <pre>{JSON.stringify(row.before_data, null, 2)}</pre>
                    </div>
                    <div>
                      <span className="muted small">Sonra</span>
                      <pre>{JSON.stringify(row.after_data, null, 2)}</pre>
                    </div>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
        {query.hasNextPage && (
          <div className="more">
            <button className="btn" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
              Daha fazla göster
            </button>
          </div>
        )}
      </Card>
    </Page>
  );
}
