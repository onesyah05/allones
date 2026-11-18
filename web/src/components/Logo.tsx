// The mark: a four-point star, one solid shape. Used for the brand and for the agent in chat.
export function StarMark({ size = 16, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <path d="M8 0c.5 4.4 3.6 7.5 8 8-4.4.5-7.5 3.6-8 8-.5-4.4-3.6-7.5-8-8 4.4-.5 7.5-3.6 8-8Z" fill="currentColor" />
    </svg>
  );
}

export function Wordmark({ size = 16 }: { size?: number }) {
  return (
    <span className="inline-flex items-center gap-2 text-[var(--color-text)]">
      <StarMark size={size} />
      <span className="font-medium" style={{ fontSize: size + 1 }}>
        AllOnes
      </span>
    </span>
  );
}
