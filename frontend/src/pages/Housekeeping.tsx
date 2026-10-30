import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Badge, Card, Empty, Field, Modal, Page, Query, useToast } from "../components/ui";
import { api } from "../lib/api";
import { dayLong, taskLabels } from "../lib/format";

interface Task {
  id: number;
  room_id: number;
  room_number: string;
  floor: number;
  hk_status: string;
  task_type: string;
  status: string;
  assignee: string | null;
  assigned_to: number | null;
  note: string;
}

const columns = [
  { key: "bekliyor", label: "Bekliyor", next: "devam", action: "Başla" },
  { key: "devam", label: "Devam ediyor", next: "tamamlandi", action: "Bitir" },
  { key: "tamamlandi", label: "Tamamlandı", next: "", action: "" },
];

export default function Housekeeping() {
  const toast = useToast();
  const client = useQueryClient();
  const [adding, setAdding] = useState(false);
  const query = useQuery({ queryKey: ["tasks"], queryFn: () => api<{ business_date: string; items: Task[] }>("/housekeeping/tasks"), refetchInterval: 20000 });
  const update = useMutation({
    mutationFn: ({ task, status }: { task: Task; status: string }) =>
      api(`/housekeeping/tasks/${task.id}`, { method: "PUT", body: { status, assigned_to: task.assigned_to, note: task.note } }),
    onSuccess: () => void client.invalidateQueries({ queryKey: ["tasks"] }),
    onError: (error) => toast(error instanceof Error ? error.message : "Görev güncellenemedi", true),
  });

  return (
    <Page
      title="Kat Hizmetleri"
      note={query.data && dayLong(query.data.business_date)}
      actions={
        <button className="btn primary" onClick={() => setAdding(true)}>
          Görev ekle
        </button>
      }
    >
      <Query query={query}>
        {(data) => (
          <div className="kanban">
            {columns.map((column) => {
              const tasks = data.items.filter((task) => task.status === column.key);
              return (
                <Card key={column.key} title={`${column.label} (${tasks.length})`} flush>
                  {tasks.length === 0 ? (
                    <Empty title="Görev yok" />
                  ) : (
                    <ul className="rows">
                      {tasks.map((task) => (
                        <li key={task.id}>
                          <div className="row-line">
                            <span>
                              <strong>
                                Oda {task.room_number}
                              </strong>
                              <span className="muted small">
                                {taskLabels[task.task_type]} · {task.floor}. kat
                              </span>
                              <Badge status={task.hk_status} />
                            </span>
                            {column.next && (
                              <button className="btn" disabled={update.isPending} onClick={() => update.mutate({ task, status: column.next })}>
                                {column.action}
                              </button>
                            )}
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </Card>
              );
            })}
          </div>
        )}
      </Query>
      {adding && <AddTask onClose={() => setAdding(false)} />}
    </Page>
  );
}

function AddTask({ onClose }: { onClose: () => void }) {
  const toast = useToast();
  const client = useQueryClient();
  const rooms = useQuery({ queryKey: ["rooms"], queryFn: () => api<{ items: { id: number; number: string }[] }>("/rooms") });
  const [room, setRoom] = useState<number>();
  const [type, setType] = useState("cikis_temizligi");
  const [note, setNote] = useState("");
  const create = useMutation({
    mutationFn: () => api("/housekeeping/tasks", { method: "POST", body: { room_id: room ?? rooms.data?.items[0].id, task_type: type, note } }),
    onSuccess: () => {
      toast("Görev eklendi");
      void client.invalidateQueries({ queryKey: ["tasks"] });
      onClose();
    },
    onError: (error) => toast(error instanceof Error ? error.message : "Görev eklenemedi", true),
  });
  return (
    <Modal title="Görev ekle" onClose={onClose}>
      <form
        className="stack"
        onSubmit={(event) => {
          event.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Oda">
          {(id) => (
            <select id={id} value={room} onChange={(e) => setRoom(Number(e.target.value))}>
              {rooms.data?.items.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.number}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Görev">
          {(id) => (
            <select id={id} value={type} onChange={(e) => setType(e.target.value)}>
              {Object.entries(taskLabels).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Not">{(id) => <input id={id} value={note} onChange={(e) => setNote(e.target.value)} />}</Field>
        <div className="form-actions">
          <button className="btn primary" disabled={create.isPending || !rooms.data}>
            Ekle
          </button>
        </div>
      </form>
    </Modal>
  );
}
