import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState, type DragEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Card, Page, Query, useToast } from "../components/ui";
import { api, ApiError } from "../lib/api";
import { addDays, dayName, dayShort, diffDays } from "../lib/format";

interface Room {
  id: number;
  number: string;
  floor: number;
  view: string;
  hk_status: string;
  room_type_id: number;
  type_code: string;
  type_name: string;
}

interface Segment {
  assignment_id: number;
  room_id: number;
  date_from: string;
  date_to: string;
  locked: boolean;
  reservation_id: number;
  code: string;
  status: string;
  guest_name: string;
  channel_code: string | null;
}

interface Unassigned {
  id: number;
  code: string;
  check_in: string;
  check_out: string;
  guest_name: string;
  type_code: string;
}

interface Board {
  rooms: Room[];
  segments: Segment[];
  unassigned: Unassigned[];
}

const CELL = 58;
const spans = [14, 21, 30];

export default function Tape() {
  const client = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [offset, setOffset] = useState(-2);
  const [span, setSpan] = useState(14);
  const [dragging, setDragging] = useState<Segment | null>(null);
  const [typeFilter, setTypeFilter] = useState("");

  const anchor = useQuery({ queryKey: ["dashboard"], queryFn: () => api<{ business_date: string }>("/dashboard") });
  const today = anchor.data?.business_date;
  const from = today ? addDays(today, offset) : undefined;
  const to = from ? addDays(from, span) : undefined;
  const board = useQuery({
    queryKey: ["calendar", from, to],
    queryFn: () => api<Board>(`/calendar?from=${from}&to=${to}`),
    enabled: Boolean(from),
    refetchInterval: 20000,
  });

  const move = useMutation({
    mutationFn: ({ reservation, room }: { reservation: number; room: number }) => api(`/reservations/${reservation}/move`, { method: "POST", body: { room_id: room } }),
    onSuccess: () => {
      toast("Oda değiştirildi");
      void client.invalidateQueries({ queryKey: ["calendar"] });
    },
    onError: (error) => toast(error instanceof ApiError ? error.message : "Oda değiştirilemedi", true),
  });

  const days = useMemo(() => (from ? Array.from({ length: span }, (_, i) => addDays(from, i)) : []), [from, span]);

  const dropOn = (event: DragEvent, room: Room) => {
    event.preventDefault();
    if (!dragging || dragging.room_id === room.id) return;
    move.mutate({ reservation: dragging.reservation_id, room: room.id });
    setDragging(null);
  };

  return (
    <Page
      title="Oda Çizelgesi"
      note="Bir rezervasyonu aynı tipteki başka bir odanın satırına sürükleyerek odasını değiştirebilirsiniz."
      actions={
        <div className="toolbar">
          <button className="btn" onClick={() => setOffset(offset - 7)} aria-label="Önceki hafta">
            ←
          </button>
          <button className="btn" onClick={() => setOffset(-2)}>
            Bugün
          </button>
          <button className="btn" onClick={() => setOffset(offset + 7)} aria-label="Sonraki hafta">
            →
          </button>
          <select value={span} onChange={(e) => setSpan(Number(e.target.value))} aria-label="Gün sayısı">
            {spans.map((value) => (
              <option key={value} value={value}>
                {value} gün
              </option>
            ))}
          </select>
          <select value={typeFilter} onChange={(e) => setTypeFilter(e.target.value)} aria-label="Oda tipi">
            <option value="">Tüm tipler</option>
            {["STD", "DLX", "FAM", "SUI"].map((code) => (
              <option key={code}>{code}</option>
            ))}
          </select>
        </div>
      }
    >
      <Query query={{ ...board, isPending: board.isPending || anchor.isPending, error: board.error ?? anchor.error }}>
        {(data) => {
          const rooms = data.rooms.filter((room) => !typeFilter || room.type_code === typeFilter);
          const bySegment = new Map<number, Segment[]>();
          data.segments.forEach((segment) => bySegment.set(segment.room_id, [...(bySegment.get(segment.room_id) ?? []), segment]));
          return (
            <>
              <div className="tape" style={{ "--cell": `${CELL}px`, "--days": span } as React.CSSProperties}>
                <div className="tape-row tape-header">
                  <div className="tape-room">Oda</div>
                  <div className="tape-days">
                    {days.map((day) => (
                      <div key={day} className={day === today ? "tape-day today" : "tape-day"}>
                        <span>{dayName(day)}</span>
                        <strong>{dayShort(day)}</strong>
                      </div>
                    ))}
                  </div>
                </div>
                {rooms.map((room) => {
                  const canDrop = dragging && dragging.room_id !== room.id && data.rooms.find((r) => r.id === dragging.room_id)?.room_type_id === room.room_type_id;
                  return (
                    <div key={room.id} className="tape-row">
                      <div className="tape-room">
                        <strong>{room.number}</strong>
                        <span className={`dot ${room.hk_status}`} title={room.hk_status} />
                        <span className="muted small">{room.type_code}</span>
                      </div>
                      <div className={canDrop ? "tape-days droppable" : "tape-days"} onDragOver={(event) => canDrop && event.preventDefault()} onDrop={(event) => dropOn(event, room)}>
                        {days.map((day) => (
                          <div key={day} className={day === today ? "tape-cell today" : "tape-cell"} />
                        ))}
                        {(bySegment.get(room.id) ?? []).map((segment) => {
                          const start = Math.max(0, diffDays(from!, segment.date_from));
                          const end = Math.min(span, diffDays(from!, segment.date_to));
                          if (end <= start) return null;
                          return (
                            <button
                              key={segment.assignment_id}
                              className={`stay ${segment.status}`}
                              draggable={segment.status === "confirmed" || segment.status === "checked_in"}
                              onDragStart={() => setDragging(segment)}
                              onDragEnd={() => setDragging(null)}
                              onClick={() => navigate(`/rezervasyonlar/${segment.reservation_id}`)}
                              title={`${segment.guest_name} · ${segment.code}${segment.channel_code ? ` · ${segment.channel_code}` : ""}`}
                              style={{ left: start * CELL + 2, width: (end - start) * CELL - 4 }}
                            >
                              {segment.locked && <span aria-label="Kilitli">●</span>} {segment.guest_name}
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  );
                })}
              </div>
              <div className="legend-row">
                <span><i className="swatch confirmed" /> Onaylı</span>
                <span><i className="swatch checked_in" /> Konaklıyor</span>
                <span>● Kilitli oda</span>
                <span><i className="dot clean" /> Temiz</span>
                <span><i className="dot dirty" /> Kirli</span>
              </div>
              {data.unassigned.length > 0 && (
                <Card title={`Odası atanmamış (${data.unassigned.length})`} aside={<Link to="/tahsis">Tahsis çalıştır</Link>} flush>
                  <ul className="rows">
                    {data.unassigned.slice(0, 12).map((row) => (
                      <li key={row.id}>
                        <Link to={`/rezervasyonlar/${row.id}`}>
                          <span>
                            <strong>{row.guest_name}</strong>
                            <span className="muted small">
                              {row.code} · {dayShort(row.check_in)} – {dayShort(row.check_out)}
                            </span>
                          </span>
                          <span className="room-tag">{row.type_code}</span>
                        </Link>
                      </li>
                    ))}
                  </ul>
                </Card>
              )}
            </>
          );
        }}
      </Query>
    </Page>
  );
}
