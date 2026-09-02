import type { ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, LoaderCircle, X } from 'lucide-react'

export function Button({children, onClick, variant='primary', disabled=false, title}: {children:ReactNode;onClick?:()=>void;variant?:'primary'|'secondary'|'danger';disabled?:boolean;title?:string}) {
  return <button title={title} disabled={disabled} onClick={onClick} className={`button ${variant === 'primary' ? 'button-primary' : variant === 'danger' ? 'button-danger' : 'button-secondary'}`}>{children}</button>
}

export function Badge({children, tone='neutral'}: {children:ReactNode;tone?:'neutral'|'success'|'warning'|'danger'|'info'}) { return <span className={`badge badge-${tone}`}>{children}</span> }

export function Empty({title, detail}: {title:string;detail?:string}) { return <div className="border-y border-slate-200 py-16 text-center"><div className="text-sm font-medium text-slate-600">{title}</div>{detail && <div className="mt-1 text-sm text-slate-400">{detail}</div>}</div> }

export function Spinner({label='处理中'}: {label?:string}) { return <span className="inline-flex items-center gap-2"><LoaderCircle size={15} className="animate-spin"/>{label}</span> }

export function Notice({kind='success', children, close}: {kind?:'success'|'error';children:ReactNode;close?:()=>void}) { const Icon = kind === 'success' ? CheckCircle2 : AlertTriangle; return <div className={`notice ${kind === 'success' ? 'notice-success' : 'notice-error'}`}><Icon size={16}/><div className="min-w-0 flex-1">{children}</div>{close && <button onClick={close}><X size={15}/></button>}</div> }

export function FieldLabel({children}: {children:ReactNode}) { return <label className="mb-1.5 block text-xs font-medium text-slate-500">{children}</label> }
