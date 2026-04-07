import Link from 'next/link';

export default function NotFound() {
  return (
    <div className="min-h-screen bg-(--background) flex items-center justify-center px-4">
      <div className="text-center max-w-sm">
        <p className="font-display text-[80px] font-bold leading-none text-(--base-03) select-none tabular-nums">
          404
        </p>
        <h1 className="font-display text-2xl font-bold text-(--base-09) mt-2">
          Page not found
        </h1>
        <p className="text-sm text-(--base-06) mt-2 mb-8">
          The page you&apos;re looking for doesn&apos;t exist or has been moved.
        </p>
        <Link
          href="/"
          className="btn btn-primary px-6 py-2.5 text-sm inline-flex items-center gap-2"
        >
          Back to Dashboard
        </Link>
      </div>
    </div>
  );
}
