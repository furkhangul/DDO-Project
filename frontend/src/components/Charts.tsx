import { useEffect, useMemo, useRef, useState } from "react";

export interface Series {
  name: string;
  color: string;
  values: number[];
}

interface LineChartProps {
  labels: string[];
  series: Series[];
  format: (value: number) => string;
  axisFormat?: (value: number) => string;
  label: string;
  max?: number;
}

const HEIGHT = 240;
const PAD = { top: 16, right: 16, bottom: 26, left: 58 };

function niceMax(value: number) {
  if (value <= 0) return 1;
  const power = 10 ** Math.floor(Math.log10(value));
  const step = [1, 2, 2.5, 5, 10].find((candidate) => candidate * power >= value) ?? 10;
  return step * power;
}

export function LineChart({ labels, series, format, axisFormat, label, max }: LineChartProps) {
  const svg = useRef<SVGSVGElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const [WIDTH, setWidth] = useState(640);
  const [hover, setHover] = useState<number | null>(null);
  useEffect(() => {
    if (!box.current) return;
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(280, Math.round(entry.contentRect.width))));
    observer.observe(box.current);
    return () => observer.disconnect();
  }, []);
  const top = useMemo(() => max ?? niceMax(Math.max(...series.flatMap((s) => s.values), 0)), [max, series]);
  const innerW = WIDTH - PAD.left - PAD.right;
  const innerH = HEIGHT - PAD.top - PAD.bottom;
  const x = (i: number) => PAD.left + (labels.length < 2 ? 0 : (i / (labels.length - 1)) * innerW);
  const y = (value: number) => PAD.top + innerH - (value / top) * innerH;
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((t) => t * top);
  const step = Math.max(1, Math.ceil(labels.length / Math.max(3, Math.floor(innerW / 80))));

  const onMove = (event: React.PointerEvent<SVGSVGElement>) => {
    const rect = svg.current?.getBoundingClientRect();
    if (!rect || labels.length === 0) return;
    const ratio = (((event.clientX - rect.left) / rect.width) * WIDTH - PAD.left) / innerW;
    setHover(Math.min(labels.length - 1, Math.max(0, Math.round(ratio * (labels.length - 1)))));
  };

  const tooltipLeft = hover === null ? 0 : (x(hover) / WIDTH) * 100;

  return (
    <figure className="chart" aria-label={label}>
      {series.length > 1 && (
        <figcaption className="legend">
          {series.map((s) => (
            <span key={s.name}>
              <i style={{ background: s.color }} aria-hidden />
              {s.name}
            </span>
          ))}
        </figcaption>
      )}
      <div className="chart-box" ref={box}>
        <svg ref={svg} viewBox={`0 0 ${WIDTH} ${HEIGHT}`} role="img" aria-label={label} onPointerMove={onMove} onPointerLeave={() => setHover(null)}>
          {ticks.map((tick) => (
            <g key={tick}>
              <line x1={PAD.left} x2={WIDTH - PAD.right} y1={y(tick)} y2={y(tick)} className="grid" />
              <text x={PAD.left - 8} y={y(tick) + 4} textAnchor="end" className="axis">
                {(axisFormat ?? format)(tick)}
              </text>
            </g>
          ))}
          {labels.map((text, i) =>
            i % step === 0 ? (
              <text key={text} x={x(i)} y={HEIGHT - 8} textAnchor="middle" className="axis">
                {text}
              </text>
            ) : null,
          )}
          {series.map((s) => (
            <g key={s.name}>
              {series.length === 1 && (
                <path d={`M${x(0)},${y(0)} ${s.values.map((v, i) => `L${x(i)},${y(v)}`).join(" ")} L${x(s.values.length - 1)},${y(0)} Z`} fill={s.color} opacity={0.1} />
              )}
              <path d={s.values.map((v, i) => `${i ? "L" : "M"}${x(i)},${y(v)}`).join(" ")} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
            </g>
          ))}
          {hover !== null && (
            <g>
              <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={PAD.top + innerH} className="crosshair" />
              {series.map((s) => (
                <circle key={s.name} cx={x(hover)} cy={y(s.values[hover])} r={4} fill={s.color} stroke="#fffdf9" strokeWidth={2} />
              ))}
            </g>
          )}
        </svg>
        {hover !== null && (
          <div className="tooltip" style={{ left: `${tooltipLeft}%`, transform: tooltipLeft > 70 ? "translateX(-105%)" : "translateX(5%)" }}>
            <span className="muted small">{labels[hover]}</span>
            {series.map((s) => (
              <span key={s.name} className="tooltip-row">
                <i style={{ background: s.color }} aria-hidden />
                <strong>{format(s.values[hover])}</strong>
                <span className="muted">{s.name}</span>
              </span>
            ))}
          </div>
        )}
      </div>
    </figure>
  );
}

export function BarList({ rows, format }: { rows: { label: string; value: number; hint?: string }[]; format: (value: number) => string }) {
  const top = Math.max(...rows.map((row) => row.value), 1);
  return (
    <ul className="barlist">
      {rows.map((row) => (
        <li key={row.label} title={row.hint}>
          <span>{row.label}</span>
          <div className="bar-track">
            <div className="bar" style={{ width: `${(row.value / top) * 100}%` }} />
          </div>
          <strong>{format(row.value)}</strong>
        </li>
      ))}
    </ul>
  );
}
