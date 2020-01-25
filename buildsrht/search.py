from srht.search import search
from buildsrht.types import Job, JobStatus

def apply_search(query, terms):
    if not terms:
        return query

    def job_image(q, v):
        return q.filter(Job.image.ilike(f"%{v}%"))

    def job_status(q, v):
        try:
            return q.filter(Job.status == JobStatus(v))
        except ValueError:
            return q.filter(False)

    def job_tags(q, v):
        return q.filter(Job.tags.ilike(f"%{v}%"))

    default_props = [Job.note]
    prop_map = {
        "image": job_image,
        "status": job_status,
        "tags": job_tags,
    }
    return search(query, terms, default_props, prop_map)