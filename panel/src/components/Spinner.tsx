import { RefreshCw } from 'lucide-react';

type SizeKey = 'xs' | 'sm' | 'md' | 'lg' | 'xl';

const sizes: Record<SizeKey, number> = {
  xs: 12,
  sm: 14,
  md: 16,
  lg: 24,
  xl: 30,
};

interface SpinnerProps {
  size?: SizeKey | number;
  className?: string;
}

export default function Spinner({ size = 'sm', className = '' }: SpinnerProps) {
  const px = typeof size === 'number' ? size : sizes[size];
  return <RefreshCw size={px} className={`animate-spin ${className}`} />;
}
