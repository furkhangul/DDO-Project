import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Link } from "react-router-dom";
import { Card, Empty, Page, Query, Stat } from "../components/ui";
import { api } from "../lib/api";
import { dayLong, dateTime } from "../lib/format";
import { describeAction, useLiveFeed, type LiveEvent } from "../lib/live";

interface Dashboard {
  hotel: string;
  business_date: string;
  counts: {
    arrivals: number;
    departures: number;
    in_house: number;
    rooms_total: number;
    rooms_dirty: number;
    rooms_out_of_order: number;
    sold_today: number;
    overbooked_nights: number;
    outbox_pending: number;
    outbox_dead: number;
    inbox_last_hour: number;
  };
  arrivals: { id: number; code: string; guest_name: string; type_code: string; room_number: string | null; status: string }[];
  departures: { id: number; code: string; guest_name: string; type_code: string; room_number: string | null; status: string; check_out: string }[];
  recent: { id: number; actor_label: string; action: string; entity: string; entity_id: string; created_at: string }[];
}

export default function DashboardPage() {
  const client = useQueryClient();
  const query = useQuery({ queryKey: ["dashboard"], queryFn: () => api<Dashboard>("/dashboard"), refetchInterval: 15000 });
  const { events, connected } = useLiveFeed(true);
  const latest = events[0]?.id;
  useEffect(() => {
    if (latest) void client.invalidateQueries({ queryKey: ["dashboard"] });
  }, [latest, client]);

  return (
    <Page
      title="Panel"
      note={query.data ? `Otel günü: ${dayLong(query.data.business_date)}` : undefined}
      actions={
        <span className={connected ? "live on" : "live"}>
          <i aria-hidden />
          {connected ? "Canlı" : "Bağlanıyor"}
        </span>
      }
    >
      <Query query={query}>
        {(data) => {
          const occupancy = data.counts.rooms_total ? Math.round((data.counts.sold_today * 100) / data.counts.rooms_total) : 0;
          return (
            <>
              <div className="stats">
                <Stat label="Doluluk" value={`%${occupancy}`} hint={`${data.counts.sold_today} / ${data.counts.rooms_total} oda`} />
                <Stat label="Bugün giriş" value={data.counts.arrivals} />
                <Stat label="Bugün çıkış" value={data.counts.departures} />
                <Stat label="Konaklayan" value={data.counts.in_house} />
                <Stat label="Kirli oda" value={data.counts.rooms_dirty} tone={data.counts.rooms_dirty > 10 ? "warn" : undefined} hint={`${data.counts.rooms_out_of_order} arızalı`} />
                <Stat
                  label="Kanal kuyruğu"
                  value={data.counts.outbox_pending}
                  tone={data.counts.outbox_dead > 0 ? "bad" : undefined}
                  hint={`${data.counts.outbox_dead} ölü, ${data.counts.inbox_last_hour} gelen/saat`}
                />
              </div>
              {data.counts.overbooked_nights > 0 && (
                <p className="banner">
                  <strong>{data.counts.overbooked_nights} gecede fazla satış var.</strong> <Link to="/fazla-satis">Yönlendirme adaylarını gör</Link>
                </p>
              )}
              <div className="grid-3">
                <Card title="Bugün gelecekler" flush>
                  <StayList rows={data.arrivals} empty="Bugün için bekleyen giriş yok" />
                </Card>
                <Card title="Bugün ayrılacaklar" flush>
                  <StayList rows={data.departures} today={data.business_date} empty="Bugün için bekleyen çıkış yok" />
                </Card>
                <Card title="Canlı akış" flush>
                  <Feed events={[...events, ...data.recent.map(fromRecent)]} />
                </Card>
              </div>
            </>
          );
        }}
      </Query>
    </Page>
  );
}

const fromRecent = (row: Dashboard["recent"][number]): LiveEvent => ({
  id: row.id,
  actor: row.actor_label,
  action: row.action,
  entity: row.entity,
  entity_id: row.entity_id,
  at: row.created_at,
});

function Feed({ events }: { events: LiveEvent[] }) {
  const unique = [...new Map(events.map((event) => [event.id, event])).values()].slice(0, 30);
  if (unique.length === 0) return <Empty title="Olay bekleniyor">Kanal simülatörü birkaç saniyede bir bildirim gönderir.</Empty>;
  return (
    <ul className="feed">
      {unique.map((event) => (
        <li key={event.id}>
          <strong>{describeAction(event.action)}</strong>
          <span className="muted small">
            {event.actor} · {dateTime(event.at)}
          </span>
        </li>
      ))}
    </ul>
  );
}

function StayList({ rows, empty, today }: { rows: (Dashboard["arrivals"][number] & { check_out?: string })[]; empty: string; today?: string }) {
  if (rows.length === 0) return <Empty title={empty} />;
  return (
    <ul className="rows">
      {rows.map((row) => (
        <li key={row.id}>
          <Link to={`/rezervasyonlar/${row.id}`}>
            <span>
              <strong>{row.guest_name}</strong>
              <span className="muted small">
                {row.code} · {row.type_code}
                {today && row.check_out && row.check_out < today && " · çıkışı gecikmiş"}
              </span>
            </span>
            <span className="room-tag">{row.room_number ?? "Atanmadı"}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

