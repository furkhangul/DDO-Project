import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Card, Empty, Field, Page, Stat, useToast } from "../components/ui";
import { api } from "../lib/api";
import { addDays } from "../lib/format";

interface Plan {
  stays: number;
  rooms: number;
  baseline_moves: number;
  applied: boolean;
  unassigned: number[];
  result: { moves: number; prefs_met: number; prefs_total: number; elapsed_ms: number; initial_cost: number; final_cost: number; iterations: number };
}

export default function Allocation() {
  const toast = useToast();
  const client = useQueryClient();
  const today = useQuery({ queryKey: ["dashboard"], queryFn: () => api<{ business_date: string }>("/dashboard") });
  const [days, setDays] = useState(30);
  const [plan, setPlan] = useState<Plan | null>(null);

  const run = useMutation({
    mutationFn: (apply: boolean) => {
      const from = today.data!.business_date;
      return api<Plan>("/assignments/solve", { method: "POST", body: { from, to: addDays(from, days), apply } });
    },
    onSuccess: (result) => {
      setPlan(result);
      if (result.applied) {
        toast("Oda planı uygulandı");
        void client.invalidateQueries({ queryKey: ["calendar"] });
      }
    },
    onError: (error) => toast(error instanceof Error ? error.message : "Çözüm üretilemedi", true),
  });

  return (
    <Page title="Oda Tahsisi" note="Çözücü kat, manzara ve bitişik oda isteklerini dikkate alır, konaklama ortasında oda değişikliğini en aza indirir. Giriş yapmış ve elle taşınmış odalar sabit kalır.">
      <Card>
        <div className="toolbar">
          <Field label="Plan ufku (gün)">{(id) => <input id={id} type="number" min={1} max={120} value={days} onChange={(e) => setDays(Number(e.target.value))} />}</Field>
          <button className="btn" disabled={run.isPending || !today.data} onClick={() => run.mutate(false)}>
            {run.isPending ? "Çözülüyor…" : "Önizle"}
          </button>
          <button className="btn primary" disabled={run.isPending || !plan || plan.applied} onClick={() => run.mutate(true)}>
            Planı uygula
          </button>
        </div>
      </Card>
      {!plan ? (
        <Card>
          <Empty title="Henüz plan yok">Önizle ile planın sonucunu kaydetmeden görebilirsiniz.</Empty>
        </Card>
      ) : (
        <>
          <div className="stats">
            <Stat label="Planlanan konaklama" value={plan.stays} hint={`${plan.rooms} oda`} />
            <Stat label="Oda değişikliği" value={plan.result.moves} hint={`İlk uygun yöntem: ${plan.baseline_moves}`} />
            <Stat label="Karşılanan istek" value={`${plan.result.prefs_met}/${plan.result.prefs_total}`} />
            <Stat label="Çözüm süresi" value={`${(plan.result.elapsed_ms / 1000).toLocaleString("tr-TR")} sn`} hint={`${plan.result.iterations.toLocaleString("tr-TR")} deneme`} />
            <Stat label="Atanamayan" value={plan.unassigned.length} tone={plan.unassigned.length ? "warn" : undefined} hint={plan.unassigned.length ? "Fazla satış olabilir" : "Hepsi yerleşti"} />
          </div>
          {plan.unassigned.length > 0 && (
            <p className="banner">
              Bazı konaklamalar yerleştirilemedi. <Link to="/fazla-satis">Fazla satış ekranına bakın</Link>
            </p>
          )}
        </>
      )}
    </Page>
  );
}
