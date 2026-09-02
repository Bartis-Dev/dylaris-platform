"use client";

import { ServiceErrorList } from '@/views/infrastructure/InfraCards';
import { useInfra } from '@/views/infrastructure/context';

export default function Page() {
    const { errors } = useInfra();
    // No guard: the streams are capped and age out, so this tab can empty out
    // under a reader who is looking at it. An empty list is the honest answer
    // and reads as "nothing has reported", which is exactly what it means.
    return <ServiceErrorList entries={errors} />;
}
