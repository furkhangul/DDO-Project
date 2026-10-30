import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Badge, Card, Page, Query, useToast } from "../components/ui";
import { api } from "../lib/api";
import { useUser } from "../lib/auth";
import { statusLabels, viewLabels } from "../lib/format";

interface Room {
  id: number;
  number: string;
  floor: number;
  view: string;
  hk_status: string;
  type_code: string;
  type_name: string;
}

interface RoomType {
  id: number;
  code: string;
  name: string;
  base_occupancy: number;
  max_occupancy: number;
  overbook_pct: number;
  room_count: number;
}

const statuses = ["clean", "dirty", "inspected", "out_of_order"];

export default function Rooms() {
  const user = useUser();
  const toast = useToast();
  const client = useQueryClient();
  const [floor, setFloor] = useState("");
  const rooms = useQuery({ queryKey: ["rooms"], queryFn: () => api<{ items: Room[]; adjacency: { room_a: number; room_b: number }[] }>("/rooms") });
  const types = useQuery({ queryKey: ["room-types"], queryFn: () => api<{ items: RoomType[] }>("/room-types") });
  const editable = user?.role === "housekeeping" || user?.role === "reception" || user?.role === "manager";
  const setStatus = useMutation({
    mutationFn: ({ id, status }: { id: number; status: string }) => api(`/rooms/${id}/status`, { method: "PUT", body: { status } }),
    onSuccess: () => void client.invalidateQueries({ queryKey: ["rooms"] }),
    onError: (error) => toast(error instanceof Error ? error.message : "Durum değiştirilemedi", true),
  });

  return (
    <Page title="Odalar">
      <Query query={types}>
        {(data) => (
          <div className="stats">
            {data.items.map((type) => (
              <div className="stat" key={type.id}>
                <span className="stat-label">{type.name}</span>
                <strong>{type.room_count}</strong>
                <span className="muted small">
                  {type.base_occupancy}–{type.max_occupancy} kişi · fazla satış %{type.overbook_pct}
                </span>
              </div>
            ))}
          </div>
        )}
      </Query>
      <Query query={rooms}>
        {(data) => {
          const floors = [...new Set(data.items.map((room) => room.floor))].sort((a, b) => a - b);
          const adjacent = new Map<number, number>();
          data.adjacency.forEach((pair) => {
            adjacent.set(pair.room_a, (adjacent.get(pair.room_a) ?? 0) + 1);
            adjacent.set(pair.room_b, (adjacent.get(pair.room_b) ?? 0) + 1);
          });
          const visible = data.items.filter((room) => !floor || String(room.floor) === floor);
          return (
            <Card
              flush
              title={`${visible.length} oda`}
              aside={
                <select value={floor} onChange={(e) => setFloor(e.target.value)} aria-label="Kat">
                  <option value="">Tüm katlar</option>
                  {floors.map((value) => (
                    <option key={value} value={value}>
                      {value}. kat
                    </option>
                  ))}
                </select>
              }
            >
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>Oda</th>
                      <th>Tip</th>
                      <th>Kat</th>
                      <th>Manzara</th>
                      <th>Bitişik</th>
                      <th>Durum</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visible.map((room) => (
                      <tr key={room.id}>
                        <td>
                          <strong>{room.number}</strong>
                        </td>
                        <td>{room.type_name}</td>
                        <td>{room.floor}</td>
                        <td>{viewLabels[room.view]}</td>
                        <td>{adjacent.get(room.id) ?? 0} oda</td>
                        <td>
                          {editable ? (
                            <select value={room.hk_status} aria-label={`Oda ${room.number} durumu`} onChange={(e) => setStatus.mutate({ id: room.id, status: e.target.value })}>
                              {statuses.map((status) => (
                                <option key={status} value={status}>
                                  {statusLabels[status]}
                                </option>
                              ))}
                            </select>
                          ) : (
                            <Badge status={room.hk_status} />
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </Card>
          );
        }}
      </Query>
    </Page>
  );
}
