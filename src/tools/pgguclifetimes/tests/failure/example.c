#define session_guc __thread __attribute__((__annotate__("session_guc")))
#define postmaster_guc __thread __attribute__((__annotate__("postmaster_guc")))
#define sighup_guc __thread __attribute__((__annotate__("sighup_guc")))

int needs_all;
static int static_needs_all;
extern int extern_needs_all;

__thread int needs_annotation;
static __thread int static_needs_annotation;
extern __thread int extern_needs_annotation;

__attribute__((__annotate__("postmaster_guc"))) int needs_thread;
static __attribute__((__annotate__("session_guc"))) int static_needs_thread;
extern __attribute__((__annotate__("sighup_guc"))) int extern_needs_thread;
