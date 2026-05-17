import Spinner from './Spinner';

interface LoadingStateProps {
  height?: string;
  size?: 'sm' | 'md' | 'lg' | 'xl';
}

export default function LoadingState({ height = 'h-40', size = 'xl' }: LoadingStateProps) {
  return (
    <div className={`flex items-center justify-center ${height} text-(--base-07)`}>
      <Spinner size={size} />
    </div>
  );
}
