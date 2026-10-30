import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Badge, Card, Empty, ErrorState, Loading, Page } from "../components/ui";
import { api } from "../lib/api";
import { can, useUser } from "../lib/auth";
import { dayShort, statusLabels, tl } from "../lib/format";

interface Row {
  id: number;
  code: string;
  guest_name: string;
  type_code: string;
  check_in: string;
  check_out: string;
  status: string;
  channel_code: string | null;
  total_net_kurus: number;
  total_tax_kurus: number;
}

interface Result {
  items: Row[];
  next_cursor: number | null;
}

const statuses = ["confirmed", "checked_in", "checked_out", "cancelled", "no_show", "walked"];

export default function Reservations() {
  const user = useUser();
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const [applied, setApplied] = useState("");
  const [status, setStatus] = useState("");

  const query = useInfiniteQuery({
    queryKey: ["reservations", applied, status],
    initialPageParam: null as number | null,
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "40" });
      if (applied) params.set("q", applied);
      if (status) params.set("status", status);
      if (pageParam) params.set("cursor", String(pageParam));
      return api<Result>(`/reservations?${params}`);
    },
    getNextPageParam: (last) => last.next_cursor,
  });
  const rows = query.data?.pages.flatMap((page) => page.items) ?? [];

  return (
    <Page
      title="Rezervasyonlar"
      actions={
        can.book(user?.role) && (
          <button className="btn primary" onClick={() => navigate("/rezervasyonlar/yeni")}>
            Yeni rezervasyon
          </button>
        )
      }
    >
      <form
        className="toolbar filters"
        onSubmit={(event) => {
          event.preventDefault();
          setApplied(search.trim());
        }}
      >
        <input placeholder="Misafir adı veya kod" value={search} onChange={(e) => setSearch(e.target.value)} aria-label="Ara" />
        <select value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Durum">
          <option value="">Tüm durumlar</option>
          {statuses.map((value) => (
            <option key={value} value={value}>
              {statusLabels[value]}
            </option>
          ))}
        </select>
        <button className="btn">Ara</button>
      </form>
      <Card flush>
        {query.isPending ? (
          <Loading />
        ) : query.error ? (
          <ErrorState error={query.error} retry={() => void query.refetch()} />
        ) : rows.length === 0 ? (
          <Empty title="Sonuç bulunamadı">Filtreleri değiştirip yeniden deneyin.</Empty>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Kod</th>
                  <th>Misafir</th>
                  <th>Oda tipi</th>
                  <th>Tarih</th>
                  <th>Kanal</th>
                  <th>Durum</th>
                  <th className="num">Toplam</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.id}>
                    <td>
                      <Link to={`/rezervasyonlar/${row.id}`}>{row.code}</Link>
                    </td>
                    <td>{row.guest_name}</td>
                    <td>{row.type_code}</td>
                    <td className="nowrap">
                      {dayShort(row.check_in)} – {dayShort(row.check_out)}
                    </td>
                    <td>{row.channel_code ?? "Direkt"}</td>
                    <td>
                      <Badge status={row.status} />
                    </td>
                    <td className="num">{tl(row.total_net_kurus + row.total_tax_kurus)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {query.hasNextPage && (
          <div className="more">
            <button className="btn" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
              {query.isFetchingNextPage ? "Yükleniyor…" : "Daha fazla göster"}
            </button>
          </div>
        )}
      </Card>
    </Page>
  );
}
